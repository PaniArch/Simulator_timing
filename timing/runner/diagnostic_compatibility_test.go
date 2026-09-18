package runner

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"
)

// Fingerprints include the RTL response-handshake / registered CTA-fire repair
// and the TokenBindings trace namespace / RTL I/O aperture classification.
// Cached-data fixtures now reside at 0x10800, outside the RTL I/O aperture.
// Includes the registered KMU start edge and separate hardware completion fields.
// The earlier pre-repair fingerprints
// deliberately no longer apply: CTA activation moves one edge and an extra
// response holding slot was removed (see rtl_response_regression_test.go).
// JSON includes every record field and ordered slice;
// the fixture fixes device identity, and does not cross epochs (Services has a
// separately documented cross-epoch inventory ordering limitation).
func TestDiagnosticTraceCompatibility(t *testing.T) {
	want := map[string]string{
		"multi/fault=false":  "0ee7889214dafc3a960f2588540c2cd59efc38f0690b086c8714ae8454b8a05d",
		"multi/fault=true":   "a4458928cb55091cead9db3333f13ca5549a870d841d0e3c073ff4529521b8b2",
		"kernel/fault=false": "ddd42137478fd6f02308441558010f6f64bef715623ff4dbf5b2ddc3beea1c0a",
		"kernel/fault=true":  "6791e66bda7ef55269b85f07e6bd84c258b16f1152ceb3c75c9a871be4c8150a",
	}
	for _, kind := range []string{"multi", "kernel"} {
		for _, fault := range []bool{false, true} {
			key := fmt.Sprintf("%s/fault=%t", kind, fault)
			t.Run(key, func(t *testing.T) {
				result, records := baselineRun(t, kind, []uint64{1}, true, fault)
				data, err := json.Marshal(struct {
					Result  baselineResult
					Records []MultiRecord
				}{result, records})
				if err != nil {
					t.Fatal(err)
				}
				got := fmt.Sprintf("%x", sha256.Sum256(data))
				if got != want[key] {
					t.Fatalf("full result/ordered trace changed: %s want %s", got, want[key])
				}
			})
		}
	}
}
