param(
    [string]$IdentityRepository = "",
    [string]$TestDatabaseURL = "postgres://identity:identity@localhost:5433/identity_test?sslmode=disable",
    [string]$FamilyTestDatabaseURL = "postgres://family_tree:family_tree@localhost:5434/family_tree_test?sslmode=disable"
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
        $env:POSTGRES_URL = $FamilyTestDatabaseURL
        go run ./cmd/migrate up | Out-Null
        if ($LASTEXITCODE -ne 0) { throw "Family test database migration failed" }
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
    $ready = Invoke-RestMethod -Method Get -Uri "$baseURL/health/ready"
    if ($ready.response -ne "OK") {
        throw "Family API readiness check failed"
    }

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

    $treeBody = @{
        name = "E2E Dynasty"
        description = "End-to-end family tree"
        locale = "ru-RU"
        timezone = "Europe/Moscow"
    } | ConvertTo-Json -Compress
    $tree = Invoke-RestMethod `
        -Method Post `
        -Uri "$baseURL/api/v1/trees" `
        -ContentType "application/json" `
        -Headers $authorization `
        -Body $treeBody
    if ($tree.access.role -ne "owner" -or $tree.tree.version -ne 1) {
        throw "Created tree did not contain owner access and version 1"
    }
    $treeID = $tree.tree.id
    $trees = Invoke-RestMethod `
        -Method Get `
        -Uri "$baseURL/api/v1/trees" `
        -Headers $authorization
    if (($trees.items | Where-Object { $_.tree.id -eq $treeID }).Count -ne 1) {
        throw "Created tree was not returned by the accessible tree list"
    }
    $treeUpdateBody = @{
        version = 1
        name = "Updated E2E Dynasty"
    } | ConvertTo-Json -Compress
    $updatedTree = Invoke-RestMethod `
        -Method Patch `
        -Uri "$baseURL/api/v1/trees/$treeID" `
        -ContentType "application/json" `
        -Headers $authorization `
        -Body $treeUpdateBody
    if ($updatedTree.tree.version -ne 2) {
        throw "Tree update did not increment the version"
    }
    $treeDeleteBody = @{version = 2} | ConvertTo-Json -Compress
    $deletedTree = Invoke-WebRequest `
        -Method Delete `
        -Uri "$baseURL/api/v1/trees/$treeID" `
        -ContentType "application/json" `
        -Headers $authorization `
        -Body $treeDeleteBody
    if ($deletedTree.StatusCode -ne 204) {
        throw "Tree delete returned $($deletedTree.StatusCode), want 204"
    }
    $hiddenTree = Invoke-WebRequest `
        -Method Get `
        -Uri "$baseURL/api/v1/trees/$treeID" `
        -Headers $authorization `
        -SkipHttpErrorCheck
    if ($hiddenTree.StatusCode -ne 404) {
        throw "Deleted tree read returned $($hiddenTree.StatusCode), want 404"
    }
    $treeRestoreBody = @{version = 3} | ConvertTo-Json -Compress
    $restoredTree = Invoke-RestMethod `
        -Method Post `
        -Uri "$baseURL/api/v1/trees/$treeID/restore" `
        -ContentType "application/json" `
        -Headers $authorization `
        -Body $treeRestoreBody
    if ($restoredTree.tree.version -ne 4) {
        throw "Tree restore did not increment the version"
    }

    $personBody = @{
        sex = "female"
        life_status = "alive"
        biography = "E2E biography"
        preferred_name = @{
            given_name = "Anna"
            family_name = "Volkonskaya"
            language_code = "en"
        }
    } | ConvertTo-Json -Compress -Depth 4
    $person = Invoke-RestMethod `
        -Method Post `
        -Uri "$baseURL/api/v1/trees/$treeID/persons" `
        -ContentType "application/json" `
        -Headers $authorization `
        -Body $personBody
    if ($person.person.version -ne 1 -or $person.preferred_name.full_text -ne "Anna Volkonskaya") {
        throw "Created person aggregate is invalid"
    }
    $personID = $person.person.id
    $persons = Invoke-RestMethod `
        -Method Get `
        -Uri "$baseURL/api/v1/trees/$treeID/persons?query=anna&limit=10" `
        -Headers $authorization
    if (($persons.items | Where-Object { $_.person.id -eq $personID }).Count -ne 1) {
        throw "Person search did not return the created person"
    }
    $personUpdateBody = @{
        version = 1
        biography = "Updated E2E biography"
        preferred_name = @{
            given_name = "Anna"
            patronymic = "Petrovna"
            family_name = "Volkonskaya"
            language_code = "en"
        }
    } | ConvertTo-Json -Compress -Depth 4
    $updatedPerson = Invoke-RestMethod `
        -Method Patch `
        -Uri "$baseURL/api/v1/trees/$treeID/persons/$personID" `
        -ContentType "application/json" `
        -Headers $authorization `
        -Body $personUpdateBody
    if ($updatedPerson.person.version -ne 2 -or $updatedPerson.preferred_name.full_text -ne "Anna Petrovna Volkonskaya") {
        throw "Person update did not update the aggregate and version"
    }
    $personDeleteBody = @{version = 2} | ConvertTo-Json -Compress
    $deletedPerson = Invoke-WebRequest `
        -Method Delete `
        -Uri "$baseURL/api/v1/trees/$treeID/persons/$personID" `
        -ContentType "application/json" `
        -Headers $authorization `
        -Body $personDeleteBody
    if ($deletedPerson.StatusCode -ne 204) {
        throw "Person delete returned $($deletedPerson.StatusCode), want 204"
    }
    $hiddenPerson = Invoke-WebRequest `
        -Method Get `
        -Uri "$baseURL/api/v1/trees/$treeID/persons/$personID" `
        -Headers $authorization `
        -SkipHttpErrorCheck
    if ($hiddenPerson.StatusCode -ne 404) {
        throw "Deleted person read returned $($hiddenPerson.StatusCode), want 404"
    }
    $personRestoreBody = @{version = 3} | ConvertTo-Json -Compress
    $restoredPerson = Invoke-RestMethod `
        -Method Post `
        -Uri "$baseURL/api/v1/trees/$treeID/persons/$personID/restore" `
        -ContentType "application/json" `
        -Headers $authorization `
        -Body $personRestoreBody
    if ($restoredPerson.person.version -ne 4) {
        throw "Person restore did not increment the version"
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

    $passwordChangeLogin = Invoke-WebRequest `
        -Method Post `
        -Uri "$baseURL/api/v1/auth/login" `
        -ContentType "application/json" `
        -Body $loginBody `
        -SessionVariable passwordChangeSession
    $passwordChangeAccess = ($passwordChangeLogin.Content | ConvertFrom-Json).access_token
    $passwordChangeAuthorization = @{Authorization = "Bearer " + $passwordChangeAccess}
    $changedPassword = "new correct horse battery staple"
    $changePasswordBody = @{
        current_password = $password
        new_password = $changedPassword
    } | ConvertTo-Json -Compress
    $changePasswordResponse = Invoke-WebRequest `
        -Method Post `
        -Uri "$baseURL/api/v1/users/me/change-password" `
        -ContentType "application/json" `
        -Headers $passwordChangeAuthorization `
        -WebSession $passwordChangeSession `
        -Body $changePasswordBody
    if ($changePasswordResponse.StatusCode -ne 204) {
        throw "Password change returned $($changePasswordResponse.StatusCode), want 204"
    }

    $oldPasswordLogin = Invoke-WebRequest `
        -Method Post `
        -Uri "$baseURL/api/v1/auth/login" `
        -ContentType "application/json" `
        -Body $loginBody `
        -SkipHttpErrorCheck
    if ($oldPasswordLogin.StatusCode -ne 401) {
        throw "Login with changed password returned $($oldPasswordLogin.StatusCode), want 401"
    }

    $changedLoginBody = @{
        email = $email
        password = $changedPassword
    } | ConvertTo-Json -Compress
    $changedLoginResponse = Invoke-WebRequest `
        -Method Post `
        -Uri "$baseURL/api/v1/auth/login" `
        -ContentType "application/json" `
        -Body $changedLoginBody `
        -SessionVariable recoverySession
    if ($changedLoginResponse.StatusCode -ne 200) {
        throw "Login after password change returned $($changedLoginResponse.StatusCode), want 200"
    }

    $forgotPasswordBody = @{email = $email} | ConvertTo-Json -Compress
    $forgotPasswordResponse = Invoke-WebRequest `
        -Method Post `
        -Uri "$baseURL/api/v1/auth/forgot-password" `
        -ContentType "application/json" `
        -Body $forgotPasswordBody
    if ($forgotPasswordResponse.StatusCode -ne 202) {
        throw "Password recovery request returned $($forgotPasswordResponse.StatusCode), want 202"
    }

    $passwordResetToken = ""
    for ($attempt = 0; $attempt -lt 40; $attempt++) {
        $identityLog = Get-Content -Raw -LiteralPath $identityOutput -ErrorAction SilentlyContinue
        $resetMatches = [regex]::Matches(
            $identityLog,
            'password_reset_url[^\r\n]*?token=([A-Za-z0-9_-]+)'
        )
        if ($resetMatches.Count -gt 0) {
            $passwordResetToken = $resetMatches[$resetMatches.Count - 1].Groups[1].Value
            break
        }
        Start-Sleep -Milliseconds 250
    }
    if ([string]::IsNullOrWhiteSpace($passwordResetToken)) {
        throw "Password reset token was not found in development mailer output"
    }

    $recoveredPassword = "recovered correct horse battery staple"
    $resetPasswordBody = @{
        token = $passwordResetToken
        new_password = $recoveredPassword
    } | ConvertTo-Json -Compress
    $resetPasswordResponse = Invoke-WebRequest `
        -Method Post `
        -Uri "$baseURL/api/v1/auth/reset-password" `
        -ContentType "application/json" `
        -Body $resetPasswordBody
    if ($resetPasswordResponse.StatusCode -ne 204) {
        throw "Password reset returned $($resetPasswordResponse.StatusCode), want 204"
    }

    $refreshAfterReset = Invoke-WebRequest `
        -Method Post `
        -Uri "$baseURL/api/v1/auth/refresh" `
        -WebSession $recoverySession `
        -SkipHttpErrorCheck
    if ($refreshAfterReset.StatusCode -ne 401) {
        throw "Refresh after password reset returned $($refreshAfterReset.StatusCode), want 401"
    }
    $reusedReset = Invoke-WebRequest `
        -Method Post `
        -Uri "$baseURL/api/v1/auth/reset-password" `
        -ContentType "application/json" `
        -Body $resetPasswordBody `
        -SkipHttpErrorCheck
    if ($reusedReset.StatusCode -ne 422) {
        throw "Reused reset token returned $($reusedReset.StatusCode), want 422"
    }
    $recoveredLoginBody = @{
        email = $email
        password = $recoveredPassword
    } | ConvertTo-Json -Compress
    $recoveredLogin = Invoke-WebRequest `
        -Method Post `
        -Uri "$baseURL/api/v1/auth/login" `
        -ContentType "application/json" `
        -Body $recoveredLoginBody
    if ($recoveredLogin.StatusCode -ne 200) {
        throw "Login after password recovery returned $($recoveredLogin.StatusCode), want 200"
    }

    $wrongLoginBody = @{
        email = $email
        password = "definitely wrong password"
    } | ConvertTo-Json -Compress
    $rateLimitStatus = 0
    for ($attempt = 0; $attempt -lt 5; $attempt++) {
        $limitedLogin = Invoke-WebRequest `
            -Method Post `
            -Uri "$baseURL/api/v1/auth/login" `
            -ContentType "application/json" `
            -Body $wrongLoginBody `
            -SkipHttpErrorCheck
        $rateLimitStatus = $limitedLogin.StatusCode
        if ($attempt -lt 4 -and $rateLimitStatus -ne 401) {
            throw "Pre-limit login returned $rateLimitStatus, want 401"
        }
    }
    if ($rateLimitStatus -ne 429) {
        throw "Login rate limit returned $rateLimitStatus, want 429"
    }
    if ([string]::IsNullOrWhiteSpace($limitedLogin.Headers["Retry-After"])) {
        throw "Login rate limit response did not include Retry-After"
    }

    [pscustomobject]@{
        health = $health.response
        readiness = $ready.response
        registered_status = $register.user.status
        verified_status = $verified.user.status
        refresh_cookie_http_only = $refreshCookie.HttpOnly
        profile_email = $profile.user.email
        tree_lifecycle_version = $restoredTree.tree.version
        person_lifecycle_version = $restoredPerson.person.version
        sessions_before_revoke = $sessions.items.Count
        refresh_after_logout_all = $afterLogout.StatusCode
        old_password_login = $oldPasswordLogin.StatusCode
        refresh_after_password_reset = $refreshAfterReset.StatusCode
        reused_reset_token = $reusedReset.StatusCode
        recovered_login = $recoveredLogin.StatusCode
        login_rate_limit = $rateLimitStatus
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
