param(
    [string]$IdentityRepository = "",
    [string]$TestDatabaseURL = "postgres://identity:identity@localhost:5433/identity_test?sslmode=disable"
)

$ErrorActionPreference = "Stop"

$familyRepository = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot ".."))
if ([string]::IsNullOrWhiteSpace($IdentityRepository)) {
    $IdentityRepository = Join-Path (Split-Path $familyRepository -Parent) "family-tree-identity-microservice"
}
$IdentityRepository = [System.IO.Path]::GetFullPath($IdentityRepository)
if (-not (Test-Path -LiteralPath (Join-Path $IdentityRepository "cmd/identity-service"))) {
    throw "Identity repository was not found at $IdentityRepository"
}
if (Get-NetTCPConnection -LocalPort 50051 -State Listen -ErrorAction SilentlyContinue) {
    throw "Port 50051 is already in use"
}
if (Get-NetTCPConnection -LocalPort 18080 -State Listen -ErrorAction SilentlyContinue) {
    throw "Port 18080 is already in use"
}

$temporaryRoot = Join-Path (
    [System.IO.Path]::GetTempPath()
) ("family-tree-e2e-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $temporaryRoot | Out-Null

$identityProcess = $null
$familyProcess = $null
try {
    $identityBinary = Join-Path $temporaryRoot "identity-service.exe"
    $familyBinary = Join-Path $temporaryRoot "family-api.exe"

    Push-Location $IdentityRepository
    try {
        go build -o $identityBinary ./cmd/identity-service
        if ($LASTEXITCODE -ne 0) { throw "Identity build failed" }
        $env:IDENTITY_POSTGRES_URL = $TestDatabaseURL
        go run ./cmd/migrate up | Out-Null
        if ($LASTEXITCODE -ne 0) { throw "Identity test database migration failed" }
    } finally {
        Pop-Location
    }

    Push-Location $familyRepository
    try {
        go build -o $familyBinary ./cmd/family_tree_app
        if ($LASTEXITCODE -ne 0) { throw "Family API build failed" }
    } finally {
        Pop-Location
    }

    $env:IDENTITY_GRPC_ADDR = "127.0.0.1:50051"
    $identityOutput = Join-Path $temporaryRoot "identity.stdout.log"
    $identityError = Join-Path $temporaryRoot "identity.stderr.log"
    $identityProcess = Start-Process `
        -FilePath $identityBinary `
        -WorkingDirectory $IdentityRepository `
        -WindowStyle Hidden `
        -PassThru `
        -RedirectStandardOutput $identityOutput `
        -RedirectStandardError $identityError

    $identityReady = $false
    for ($attempt = 0; $attempt -lt 80; $attempt++) {
        if (Get-NetTCPConnection -LocalPort 50051 -State Listen -ErrorAction SilentlyContinue) {
            $identityReady = $true
            break
        }
        Start-Sleep -Milliseconds 250
    }
    if (-not $identityReady) { throw "Identity service did not start" }

    $env:HTTP_ADDR = "127.0.0.1:18080"
    $env:LOGGER_FOLDER = Join-Path $temporaryRoot "family-logs"
    $env:AUTH_REFRESH_COOKIE_SECURE = "false"
    $familyOutput = Join-Path $temporaryRoot "family.stdout.log"
    $familyError = Join-Path $temporaryRoot "family.stderr.log"
    $familyProcess = Start-Process `
        -FilePath $familyBinary `
        -WorkingDirectory $familyRepository `
        -WindowStyle Hidden `
        -PassThru `
        -RedirectStandardOutput $familyOutput `
        -RedirectStandardError $familyError

    $baseURL = "http://127.0.0.1:18080"
    $familyReady = $false
    for ($attempt = 0; $attempt -lt 80; $attempt++) {
        try {
            $health = Invoke-RestMethod -Method Get -Uri "$baseURL/health/live"
            if ($health.response -eq "OK") {
                $familyReady = $true
                break
            }
        } catch {
            # The process is still starting.
        }
        Start-Sleep -Milliseconds 250
    }
    if (-not $familyReady) { throw "Family API did not start" }

    $email = "e2e-" + [guid]::NewGuid().ToString("N") + "@example.com"
    $password = "correct horse battery staple"
    $registerBody = @{
        email = $email
        password = $password
        display_name = "E2E Family"
    } | ConvertTo-Json -Compress
    $register = Invoke-RestMethod `
        -Method Post `
        -Uri "$baseURL/api/v1/auth/register" `
        -ContentType "application/json" `
        -Body $registerBody
    if (-not $register.verification_required) {
        throw "Registration did not require verification"
    }

    $verificationToken = ""
    for ($attempt = 0; $attempt -lt 40; $attempt++) {
        $identityLog = Get-Content -Raw -LiteralPath $identityOutput -ErrorAction SilentlyContinue
        $pattern = [regex]::Escape($email) + ".*?token=([A-Za-z0-9_-]+)"
        $tokenMatch = [regex]::Match($identityLog, $pattern)
        if ($tokenMatch.Success) {
            $verificationToken = $tokenMatch.Groups[1].Value
            break
        }
        Start-Sleep -Milliseconds 250
    }
    if ([string]::IsNullOrWhiteSpace($verificationToken)) {
        throw "Verification token was not found in development mailer output"
    }

    $verifyBody = @{token = $verificationToken} | ConvertTo-Json -Compress
    $verified = Invoke-RestMethod `
        -Method Post `
        -Uri "$baseURL/api/v1/auth/verify-email" `
        -ContentType "application/json" `
        -Body $verifyBody
    if ($verified.user.status -ne "active") {
        throw "Email verification did not activate the user"
    }

    $loginBody = @{email = $email; password = $password} | ConvertTo-Json -Compress
    $loginResponse = Invoke-WebRequest `
        -Method Post `
        -Uri "$baseURL/api/v1/auth/login" `
        -ContentType "application/json" `
        -Body $loginBody `
        -SessionVariable browserSession
    $login = $loginResponse.Content | ConvertFrom-Json
    if ([string]::IsNullOrWhiteSpace($login.access_token)) {
        throw "Login access token is empty"
    }
    if ($loginResponse.Content -match "refresh_token") {
        throw "Refresh token leaked into login JSON"
    }
    $refreshCookie = $browserSession.Cookies.GetCookies(
        "$baseURL/api/v1/auth/refresh"
    )["family_tree_refresh"]
    if ($null -eq $refreshCookie -or -not $refreshCookie.HttpOnly) {
        throw "Protected refresh cookie was not set"
    }

    $refreshResponse = Invoke-WebRequest `
        -Method Post `
        -Uri "$baseURL/api/v1/auth/refresh" `
        -WebSession $browserSession
    $refresh = $refreshResponse.Content | ConvertFrom-Json
    if ([string]::IsNullOrWhiteSpace($refresh.access_token)) {
        throw "Refresh access token is empty"
    }

    $authorization = @{Authorization = "Bearer " + $refresh.access_token}
    $profile = Invoke-RestMethod `
        -Method Get `
        -Uri "$baseURL/api/v1/users/me" `
        -Headers $authorization
    if ($profile.user.email -ne $email) {
        throw "Current-user profile does not match the authenticated account"
    }

    $secondLoginResponse = Invoke-WebRequest `
        -Method Post `
        -Uri "$baseURL/api/v1/auth/login" `
        -ContentType "application/json" `
        -Body $loginBody `
        -SessionVariable secondBrowserSession
    if ($secondLoginResponse.StatusCode -ne 200) {
        throw "Second session could not be created"
    }
    $sessions = Invoke-RestMethod `
        -Method Get `
        -Uri "$baseURL/api/v1/users/me/sessions" `
        -Headers $authorization
    if ($sessions.items.Count -ne 2) {
        throw "Active session count is $($sessions.items.Count), want 2"
    }
    $otherSession = $sessions.items | Where-Object { -not $_.current } | Select-Object -First 1
    if ($null -eq $otherSession) {
        throw "Session list did not identify the current session"
    }
    $revokeOther = Invoke-WebRequest `
        -Method Delete `
        -Uri "$baseURL/api/v1/users/me/sessions/$($otherSession.id)" `
        -Headers $authorization
    if ($revokeOther.StatusCode -ne 204) {
        throw "Selected session revoke returned $($revokeOther.StatusCode), want 204"
    }

    $logoutAll = Invoke-RestMethod `
        -Method Post `
        -Uri "$baseURL/api/v1/auth/logout-all" `
        -Headers $authorization `
        -WebSession $browserSession
    if ($logoutAll.revoked_session_count -lt 1) {
        throw "Logout-all did not revoke a session"
    }

    $afterLogout = Invoke-WebRequest `
        -Method Post `
        -Uri "$baseURL/api/v1/auth/refresh" `
        -WebSession $browserSession `
        -SkipHttpErrorCheck
    if ($afterLogout.StatusCode -ne 401) {
        throw "Refresh after logout-all returned $($afterLogout.StatusCode), want 401"
    }

    [pscustomobject]@{
        health = $health.response
        registered_status = $register.user.status
        verified_status = $verified.user.status
        refresh_cookie_http_only = $refreshCookie.HttpOnly
        profile_email = $profile.user.email
        sessions_before_revoke = $sessions.items.Count
        refresh_after_logout_all = $afterLogout.StatusCode
    }
} catch {
    foreach ($logName in @(
        "identity.stdout.log",
        "identity.stderr.log",
        "family.stdout.log",
        "family.stderr.log"
    )) {
        $logPath = Join-Path $temporaryRoot $logName
        if (Test-Path -LiteralPath $logPath) {
            Get-Content -LiteralPath $logPath -Tail 30
        }
    }
    throw
} finally {
    if ($null -ne $familyProcess -and -not $familyProcess.HasExited) {
        Stop-Process -Id $familyProcess.Id -Force
    }
    if ($null -ne $identityProcess -and -not $identityProcess.HasExited) {
        Stop-Process -Id $identityProcess.Id -Force
    }

    $resolvedTemp = [System.IO.Path]::GetFullPath([System.IO.Path]::GetTempPath())
    $resolvedRun = [System.IO.Path]::GetFullPath($temporaryRoot)
    if (-not $resolvedRun.StartsWith(
        $resolvedTemp,
        [System.StringComparison]::OrdinalIgnoreCase
    )) {
        throw "Refusing to clean unexpected path $resolvedRun"
    }
    Remove-Item -LiteralPath $resolvedRun -Recurse -Force
}
