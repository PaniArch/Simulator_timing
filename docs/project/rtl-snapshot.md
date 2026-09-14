# Bundled RTL snapshot

The cycle baseline carries its frozen RTL evidence under repository-root
`Vortex_rtl/`. Architecture and later timing work must resolve RTL references
against this directory, without relying on another checkout.

- snapshot commit: `85a88fe250b0da483cb33ed34126d675ecb93c1c`
- contained Vortex source revision: `e2b9745b637ce8ac462be2f0e01b5d76542dc6c0`
- copied working-tree files: 394
- nested Git metadata: excluded
- baseline use: read-only configuration and RTL evidence

The snapshot is reference input. All RTL, configuration, and helper-source
contents remain unchanged. Only the bundled README is localized for this
repository, with its `MANIFEST.sha256` entry updated. Baseline preparation does
not derive any cycle parameters from the snapshot.
