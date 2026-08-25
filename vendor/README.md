# Vendor directory

E0 currently has no imported third-party Go package. This directory is kept so
the repository makes that state explicit; `go test -mod=vendor ./...` is still
the required offline test mode. When an approved dependency gains a real import,
replace this placeholder with the output of `go mod vendor`.

