param(
    [Parameter(Mandatory = $true)][string]$BackupDirectory,
    [string]$SourcePostgresURL = $env:POSTGRES_URL,
    [string]$SourceBucket = $env:S3_BUCKET,
    [string]$TargetPostgresURL = "",
    [string]$TargetBucket = "",
    [string]$S3Endpoint = $env:S3_ENDPOINT,
    [string]$S3Region = $env:S3_REGION,
    [string]$PgBinDirectory = "",
    [ValidateSet("native", "docker")][string]$PostgresClientMode = "native",
    [string]$PostgresClientImage = "postgres:17-alpine",
    [switch]$CreateOnly,
    [switch]$ConfirmQuiesced
)

$ErrorActionPreference = "Stop"

function Get-NativeToolPath {
    param([string]$Name)

    if (-not [string]::IsNullOrWhiteSpace($PgBinDirectory) -and
        $Name -in @("pg_dump", "pg_restore", "psql")) {
        $extension = if ($IsWindows) { ".exe" } else { "" }
        $candidate = Join-Path $PgBinDirectory ($Name + $extension)
        if (-not (Test-Path -LiteralPath $candidate -PathType Leaf)) {
            throw "$Name was not found in PgBinDirectory"
        }
        return [System.IO.Path]::GetFullPath($candidate)
    }
    $command = Get-Command $Name -CommandType Application -ErrorAction SilentlyContinue |
        Select-Object -First 1
    if ($null -eq $command) {
        throw "$Name is required but was not found in PATH"
    }
    return $command.Path
}

function Invoke-PostgresTool {
    param(
        [Parameter(Mandatory = $true)][string]$DatabaseURL,
        [Parameter(Mandatory = $true)][string]$Executable,
        [Parameter(Mandatory = $true)][string[]]$Arguments
    )

    try {
        $uri = [System.Uri]::new($DatabaseURL)
    } catch {
        throw "PostgreSQL URL is invalid"
    }
    if ($uri.Scheme -notin @("postgres", "postgresql") -or
        [string]::IsNullOrWhiteSpace($uri.Host) -or
        [string]::IsNullOrWhiteSpace($uri.AbsolutePath.TrimStart("/"))) {
        throw "PostgreSQL URL must contain scheme, host and database"
    }
    $credentials = $uri.UserInfo.Split(":", 2)
    if ($credentials.Count -eq 0 -or [string]::IsNullOrWhiteSpace($credentials[0])) {
        throw "PostgreSQL URL must contain a user"
    }
    $values = @{
        PGHOST = $uri.Host
        PGPORT = if ($uri.Port -gt 0) { [string]$uri.Port } else { "5432" }
        PGDATABASE = [System.Uri]::UnescapeDataString($uri.AbsolutePath.TrimStart("/"))
        PGUSER = [System.Uri]::UnescapeDataString($credentials[0])
        PGPASSWORD = if ($credentials.Count -eq 2) {
            [System.Uri]::UnescapeDataString($credentials[1])
        } else { "" }
        PGCONNECT_TIMEOUT = "10"
    }
    foreach ($pair in $uri.Query.TrimStart("?").Split("&", [System.StringSplitOptions]::RemoveEmptyEntries)) {
        $parts = $pair.Split("=", 2)
        if ($parts.Count -eq 2 -and $parts[0] -eq "sslmode") {
            $values.PGSSLMODE = [System.Uri]::UnescapeDataString($parts[1])
        }
    }
    if ($PostgresClientMode -eq "docker") {
        $dockerArguments = @(
            "run", "--rm", "--network", "host",
            "--mount", "type=bind,source=$script:BackupRoot,target=/backup"
        )
        foreach ($name in $values.Keys) {
            $dockerArguments += "--env"
            $dockerArguments += ("{0}={1}" -f $name, [string]$values[$name])
        }
        $dockerArguments += "--entrypoint"
        $dockerArguments += $Executable
        $dockerArguments += $PostgresClientImage
        $output = & $script:DockerExecutable @dockerArguments @Arguments
    } else {
        $previous = @{}
        foreach ($name in $values.Keys) {
            $previous[$name] = [System.Environment]::GetEnvironmentVariable($name, "Process")
            [System.Environment]::SetEnvironmentVariable($name, [string]$values[$name], "Process")
        }
        try {
            $output = & $Executable @Arguments
        } finally {
            foreach ($name in $values.Keys) {
                [System.Environment]::SetEnvironmentVariable($name, $previous[$name], "Process")
            }
        }
    }
    if ($LASTEXITCODE -ne 0) {
        throw "PostgreSQL tool failed with exit code $LASTEXITCODE"
    }
    return @($output)
}

function Get-PostgresClientPath {
    param([Parameter(Mandatory = $true)][string]$HostPath)

    if ($PostgresClientMode -ne "docker") {
        return $HostPath
    }
    $relativePath = [System.IO.Path]::GetRelativePath($script:BackupRoot, $HostPath)
    if ($relativePath -eq ".." -or $relativePath.StartsWith(".." + [System.IO.Path]::DirectorySeparatorChar)) {
        throw "PostgreSQL client path must stay within BackupDirectory"
    }
    return "/backup/" + $relativePath.Replace("\", "/")
}

function Invoke-S3 {
    param([Parameter(Mandatory = $true)][string[]]$Arguments)

    $output = & $script:AwsExecutable `
        --no-cli-pager `
        --endpoint-url $S3Endpoint `
        --region $S3Region `
        @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "AWS CLI S3 operation failed with exit code $LASTEXITCODE"
    }
    return @($output)
}

function Resolve-ObjectFile {
    param(
        [Parameter(Mandatory = $true)][string]$Root,
        [Parameter(Mandatory = $true)][string]$Key
    )

    if ([string]::IsNullOrWhiteSpace($Key) -or $Key.StartsWith("/") -or $Key.StartsWith("\")) {
        throw "Unsafe S3 object key"
    }
    $segments = $Key -split "[/\\]"
    if ($segments.Count -eq 0 -or @($segments | Where-Object { $_ -in @("", ".", "..") }).Count -gt 0) {
        throw "Unsafe S3 object key"
    }
    $relative = [string]::Join([System.IO.Path]::DirectorySeparatorChar, $segments)
    $resolvedRoot = [System.IO.Path]::GetFullPath($Root)
    $resolved = [System.IO.Path]::GetFullPath((Join-Path $resolvedRoot $relative))
    $prefix = $resolvedRoot.TrimEnd(
        [System.IO.Path]::DirectorySeparatorChar,
        [System.IO.Path]::AltDirectorySeparatorChar
    ) + [System.IO.Path]::DirectorySeparatorChar
    if (-not $resolved.StartsWith($prefix, [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "S3 object key escapes the backup directory"
    }
    return $resolved
}

function Get-DatabaseState {
    param([string]$DatabaseURL)

    $migrationOutput = Invoke-PostgresTool $DatabaseURL $script:PsqlExecutable @(
        "-X", "--no-psqlrc", "--tuples-only", "--no-align",
        "--command", "SELECT COALESCE(MAX(version), 0) FROM schema_migrations"
    )
    $migrationVersion = [int](($migrationOutput -join "").Trim())
    $countQuery = @"
SELECT 'schema_migrations|' || COUNT(*) FROM schema_migrations
UNION ALL SELECT 'family_trees|' || COUNT(*) FROM family_trees
UNION ALL SELECT 'tree_members|' || COUNT(*) FROM tree_members
UNION ALL SELECT 'persons|' || COUNT(*) FROM persons
UNION ALL SELECT 'person_names|' || COUNT(*) FROM person_names
UNION ALL SELECT 'parent_child_relations|' || COUNT(*) FROM parent_child_relations
UNION ALL SELECT 'family_unions|' || COUNT(*) FROM family_unions
UNION ALL SELECT 'union_members|' || COUNT(*) FROM union_members
UNION ALL SELECT 'media_assets|' || COUNT(*) FROM media_assets
UNION ALL SELECT 'media_variants|' || COUNT(*) FROM media_variants
UNION ALL SELECT 'person_media|' || COUNT(*) FROM person_media
UNION ALL SELECT 'background_jobs|' || COUNT(*) FROM background_jobs
UNION ALL SELECT 'export_jobs|' || COUNT(*) FROM export_jobs
UNION ALL SELECT 'audit_log|' || COUNT(*) FROM audit_log
ORDER BY 1
"@
    $countOutput = Invoke-PostgresTool $DatabaseURL $script:PsqlExecutable @(
        "-X", "--no-psqlrc", "--tuples-only", "--no-align", "--command", $countQuery
    )
    $counts = [ordered]@{}
    foreach ($line in $countOutput) {
        $parts = ([string]$line).Trim().Split("|", 2)
        if ($parts.Count -eq 2) {
            $counts[$parts[0]] = [int64]$parts[1]
        }
    }
    return [pscustomobject]@{
        migration_version = $migrationVersion
        table_counts = $counts
    }
}

function Assert-EmptyTargetDatabase {
    param([string]$DatabaseURL)

    $output = Invoke-PostgresTool $DatabaseURL $script:PsqlExecutable @(
        "-X", "--no-psqlrc", "--tuples-only", "--no-align",
        "--command", "SELECT COUNT(*) FROM pg_tables WHERE schemaname = 'public'"
    )
    if ([int](($output -join "").Trim()) -ne 0) {
        throw "Target PostgreSQL database must have an empty public schema"
    }
}

if (-not $ConfirmQuiesced) {
    throw "Stop Family API and worker writes, then pass -ConfirmQuiesced"
}
foreach ($required in @($SourcePostgresURL, $SourceBucket, $S3Endpoint, $S3Region)) {
    if ([string]::IsNullOrWhiteSpace($required)) {
        throw "Source PostgreSQL and S3 settings are required"
    }
}
if (-not $CreateOnly) {
    if ([string]::IsNullOrWhiteSpace($TargetPostgresURL) -or
        [string]::IsNullOrWhiteSpace($TargetBucket)) {
        throw "TargetPostgresURL and TargetBucket are required for a restore drill"
    }
    if ($SourcePostgresURL -eq $TargetPostgresURL -or $SourceBucket -eq $TargetBucket) {
        throw "Restore targets must differ from backup sources"
    }
}

if ($PostgresClientMode -eq "docker") {
    if (-not $IsLinux) {
        throw "PostgresClientMode=docker is supported only on Linux hosts"
    }
    if ([string]::IsNullOrWhiteSpace($PostgresClientImage)) {
        throw "PostgresClientImage is required when PostgresClientMode=docker"
    }
    $script:DockerExecutable = Get-NativeToolPath "docker"
    $script:PgDumpExecutable = "pg_dump"
    $script:PgRestoreExecutable = "pg_restore"
    $script:PsqlExecutable = "psql"
} else {
    $script:PgDumpExecutable = Get-NativeToolPath "pg_dump"
    $script:PgRestoreExecutable = Get-NativeToolPath "pg_restore"
    $script:PsqlExecutable = Get-NativeToolPath "psql"
}
$script:AwsExecutable = Get-NativeToolPath "aws"
if ([string]::IsNullOrWhiteSpace($env:AWS_ACCESS_KEY_ID) -and
    -not [string]::IsNullOrWhiteSpace($env:S3_ACCESS_KEY_ID)) {
    $env:AWS_ACCESS_KEY_ID = $env:S3_ACCESS_KEY_ID
}
if ([string]::IsNullOrWhiteSpace($env:AWS_SECRET_ACCESS_KEY) -and
    -not [string]::IsNullOrWhiteSpace($env:S3_SECRET_ACCESS_KEY)) {
    $env:AWS_SECRET_ACCESS_KEY = $env:S3_SECRET_ACCESS_KEY
}
if ([string]::IsNullOrWhiteSpace($env:AWS_ACCESS_KEY_ID) -or
    [string]::IsNullOrWhiteSpace($env:AWS_SECRET_ACCESS_KEY)) {
    throw "AWS/S3 credentials are required in process environment variables"
}

$backupRoot = [System.IO.Path]::GetFullPath($BackupDirectory)
if ($backupRoot -eq [System.IO.Path]::GetPathRoot($backupRoot)) {
    throw "BackupDirectory cannot be a filesystem root"
}
if (Test-Path -LiteralPath $backupRoot) {
    if (@(Get-ChildItem -LiteralPath $backupRoot -Force).Count -gt 0) {
        throw "BackupDirectory must not contain existing files"
    }
} else {
    New-Item -ItemType Directory -Path $backupRoot | Out-Null
}
$objectsRoot = Join-Path $backupRoot "objects"
New-Item -ItemType Directory -Path $objectsRoot | Out-Null
$script:BackupRoot = $backupRoot
$dumpPath = Join-Path $backupRoot "postgres.dump"
$dumpClientPath = Get-PostgresClientPath $dumpPath
$manifestPath = Join-Path $backupRoot "manifest.json"

Invoke-S3 @("s3api", "head-bucket", "--bucket", $SourceBucket) | Out-Null
$sourceDatabase = Get-DatabaseState $SourcePostgresURL
Invoke-PostgresTool $SourcePostgresURL $script:PgDumpExecutable @(
    "--format=custom",
    "--no-owner",
    "--no-privileges",
    "--file", $dumpClientPath
) | Out-Null
$dumpChecksum = (Get-FileHash -Algorithm SHA256 -LiteralPath $dumpPath).Hash.ToLowerInvariant()

$listJSON = (Invoke-S3 @(
    "s3api", "list-objects-v2", "--bucket", $SourceBucket, "--output", "json"
)) -join [System.Environment]::NewLine
$listed = $listJSON | ConvertFrom-Json
$inventory = @()
foreach ($entry in @($listed.Contents | Sort-Object Key)) {
    if ($null -eq $entry) { continue }
    $key = [string]$entry.Key
    $filePath = Resolve-ObjectFile $objectsRoot $key
    $parent = Split-Path $filePath -Parent
    if (-not (Test-Path -LiteralPath $parent)) {
        New-Item -ItemType Directory -Path $parent | Out-Null
    }
    $headJSON = (Invoke-S3 @(
        "s3api", "head-object", "--bucket", $SourceBucket, "--key", $key, "--output", "json"
    )) -join [System.Environment]::NewLine
    $head = $headJSON | ConvertFrom-Json
    $metadataChecksum = [string]$head.Metadata.sha256
    if ($metadataChecksum -notmatch "^[a-f0-9]{64}$" -or
        [string]::IsNullOrWhiteSpace([string]$head.ContentType)) {
        throw "S3 object metadata is incomplete for a backed-up object"
    }
    Invoke-S3 @(
        "s3api", "get-object", "--bucket", $SourceBucket, "--key", $key, $filePath
    ) | Out-Null
    $actualSize = (Get-Item -LiteralPath $filePath).Length
    $actualChecksum = (Get-FileHash -Algorithm SHA256 -LiteralPath $filePath).Hash.ToLowerInvariant()
    if ($actualSize -ne [int64]$head.ContentLength -or $actualChecksum -ne $metadataChecksum) {
        throw "S3 object size or checksum verification failed"
    }
    $inventory += [pscustomobject]@{
        key = $key
        size_bytes = [int64]$actualSize
        sha256 = $actualChecksum
        content_type = [string]$head.ContentType
    }
}

$manifest = [ordered]@{
    schema = [ordered]@{ name = "family_tree_infrastructure_backup"; version = 1 }
    created_at = [DateTime]::UtcNow.ToString("o")
    database = [ordered]@{
        dump_file = "postgres.dump"
        sha256 = $dumpChecksum
        migration_version = $sourceDatabase.migration_version
        table_counts = $sourceDatabase.table_counts
    }
    object_storage = [ordered]@{
        source_bucket = $SourceBucket
        object_count = $inventory.Count
        objects = $inventory
    }
}
$manifest | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath $manifestPath -Encoding utf8NoBOM

if ($CreateOnly) {
    [pscustomobject]@{
        backup_directory = $backupRoot
        migration_version = $sourceDatabase.migration_version
        object_count = $inventory.Count
        postgres_sha256 = $dumpChecksum
    }
    return
}

Assert-EmptyTargetDatabase $TargetPostgresURL
Invoke-S3 @("s3api", "head-bucket", "--bucket", $TargetBucket) | Out-Null
$targetListJSON = (Invoke-S3 @(
    "s3api", "list-objects-v2", "--bucket", $TargetBucket, "--max-items", "1", "--output", "json"
)) -join [System.Environment]::NewLine
$targetList = $targetListJSON | ConvertFrom-Json
if (@($targetList.Contents).Count -gt 0) {
    throw "Target S3 bucket must be empty"
}

Invoke-PostgresTool $TargetPostgresURL $script:PgRestoreExecutable @(
    "--exit-on-error",
    "--single-transaction",
    "--no-owner",
    "--no-privileges",
    "--dbname", ([System.Uri]::new($TargetPostgresURL).AbsolutePath.TrimStart("/")),
    $dumpClientPath
) | Out-Null
$targetDatabase = Get-DatabaseState $TargetPostgresURL
if ($targetDatabase.migration_version -ne $sourceDatabase.migration_version) {
    throw "Restored PostgreSQL migration version differs from the source"
}
foreach ($name in $sourceDatabase.table_counts.Keys) {
    if ([int64]$targetDatabase.table_counts[$name] -ne [int64]$sourceDatabase.table_counts[$name]) {
        throw "Restored PostgreSQL table counts differ from the source"
    }
}

$verificationRoot = Join-Path $backupRoot "restore-verification"
New-Item -ItemType Directory -Path $verificationRoot | Out-Null
foreach ($object in $inventory) {
    $sourceFile = Resolve-ObjectFile $objectsRoot $object.key
    Invoke-S3 @(
        "s3api", "put-object",
        "--bucket", $TargetBucket,
        "--key", $object.key,
        "--body", $sourceFile,
        "--content-type", $object.content_type,
        "--metadata", ("sha256=" + $object.sha256)
    ) | Out-Null
    $targetHeadJSON = (Invoke-S3 @(
        "s3api", "head-object", "--bucket", $TargetBucket, "--key", $object.key, "--output", "json"
    )) -join [System.Environment]::NewLine
    $targetHead = $targetHeadJSON | ConvertFrom-Json
    if ([int64]$targetHead.ContentLength -ne [int64]$object.size_bytes -or
        [string]$targetHead.Metadata.sha256 -ne [string]$object.sha256 -or
        [string]$targetHead.ContentType -ne [string]$object.content_type) {
        throw "Restored S3 object metadata differs from the backup"
    }
    $verificationFile = Resolve-ObjectFile $verificationRoot $object.key
    $verificationParent = Split-Path $verificationFile -Parent
    if (-not (Test-Path -LiteralPath $verificationParent)) {
        New-Item -ItemType Directory -Path $verificationParent | Out-Null
    }
    Invoke-S3 @(
        "s3api", "get-object", "--bucket", $TargetBucket, "--key", $object.key, $verificationFile
    ) | Out-Null
    $restoredChecksum = (Get-FileHash -Algorithm SHA256 -LiteralPath $verificationFile).Hash.ToLowerInvariant()
    if ($restoredChecksum -ne [string]$object.sha256) {
        throw "Restored S3 object checksum differs from the backup"
    }
}

$resolvedVerification = [System.IO.Path]::GetFullPath($verificationRoot)
$backupPrefix = $backupRoot.TrimEnd(
    [System.IO.Path]::DirectorySeparatorChar,
    [System.IO.Path]::AltDirectorySeparatorChar
) + [System.IO.Path]::DirectorySeparatorChar
if (-not $resolvedVerification.StartsWith($backupPrefix, [System.StringComparison]::OrdinalIgnoreCase)) {
    throw "Refusing to clean an unexpected verification directory"
}
Remove-Item -LiteralPath $resolvedVerification -Recurse -Force

[pscustomobject]@{
    backup_directory = $backupRoot
    migration_version = $targetDatabase.migration_version
    verified_table_count = $sourceDatabase.table_counts.Count
    verified_object_count = $inventory.Count
    postgres_sha256 = $dumpChecksum
}
