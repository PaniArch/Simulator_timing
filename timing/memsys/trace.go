package memsys

// Transfer records a real accepted word fragment, not an offered request.
// global-adapter links Parent (SIMD transaction) and Batch to Request.Identity
// (adapter wire transaction). data-cache uses that same wire identity/tag/port
// after the port-0 buffer delay. Local Port is the logical lane. Fetch requests
// use their own transaction namespace. Cache-generated writebacks are not ISA
// instructions and must not be counted as commits.
type Transfer struct {
	Boundary string
	Port     int
	Request  WordRequest
	Parent   Identity
	Batch    BatchID
	LaneMask uint8
}
