$ErrorActionPreference = "Stop"

$goBin = go env GOBIN
if ([string]::IsNullOrWhiteSpace($goBin)) {
    $goBin = Join-Path (go env GOPATH) "bin"
}

$env:PATH = "$goBin;$env:PATH"

protoc `
    -I api/proto `
    --go_out=. `
    --go_opt=module=github.com/ZheglY/family_tree_app `
    --go-grpc_out=. `
    --go-grpc_opt=module=github.com/ZheglY/family_tree_app `
    api/proto/identity/v1/identity.proto
