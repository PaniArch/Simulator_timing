# Vendor directory

The project currently has no imported third-party Go module. Berkeley SoftFloat
is a pinned C dependency supplied by the repository-owned, ignored environment
capsule and is not part of Go vendoring. Normal development and Harness runs
link its prebuilt read-only archive rather than rebuilding it. This directory
keeps the empty Go vendor state explicit;
`go test -mod=vendor ./...` remains the required offline mode. When an approved
Go dependency gains a real import, replace this placeholder with `go mod vendor`
output.
