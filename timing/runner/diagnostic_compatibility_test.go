package runner

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"
)

// Fingerprints were captured from the state-construction-cost parent before
// diagnostic optimization. JSON includes every record field and ordered slice;
// the fixture fixes device identity, and does not cross epochs (Services has a
// separately documented cross-epoch inventory ordering limitation).
func TestDiagnosticTraceCompatibility(t *testing.T) {
	want := map[string]string{
		"multi/fault=false":  "a5d1f03d1219b62db4d33d606fb7571c5cc3e9edd00c35aaadb1f438ec2f970f",
		"multi/fault=true":   "9df8a12a605ac385cb512987c3b0411a7ef0bde7c683b4a7fca6f4e72b9e2bac",
		"kernel/fault=false": "94b802f0ca665dd2b00fc3e18b59a219bea6a7ff549d2610b62a2021e92ea1f3",
		"kernel/fault=true":  "b36a91dd1e0303bcae49b6e8635a34bab415bf26bbf0acee6a367e11667d2ed2",
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
