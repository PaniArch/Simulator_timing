# Vendor directory

The project currently has no imported third-party Go module. Berkeley SoftFloat
is a C dependency built from the existing Vortex submodule and is not part of Go
vendoring. This directory keeps the empty Go vendor state explicit;
`go test -mod=vendor ./...` remains the required offline mode. When an approved
Go dependency gains a real import, replace this placeholder with `go mod vendor`
output.

