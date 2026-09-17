package timing

import (
	"fmt"
	"sync"
	"testing"
)

func TestNumberSharedIRIsolation(t *testing.T) {
	// Independent inputs must still see changed values, invalid types and parse
	// failures after the embedded document has been cached.
	want, err := Number("resources", "res-lsu-load-tags", "capacity")
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"17", "0", "null", "'3'", "999999999999999999999999999999999999"} {
		data := []byte("resources:\n- id: res-lsu-load-tags\n  capacity: " + value + "\n")
		got, err := number(data, "resources", "res-lsu-load-tags", "capacity")
		if value == "17" || value == "0" {
			if err != nil || fmt.Sprint(got) != value {
				t.Fatalf("changed input: %d %v", got, err)
			}
		} else if err == nil {
			t.Fatalf("accepted %s", value)
		}
		if got, err := Number("resources", "res-lsu-load-tags", "capacity"); err != nil || got != want {
			t.Fatalf("cache polluted: %d %v", got, err)
		}
	}
	if _, err := number([]byte("["), "resources", "x", "capacity"); err == nil {
		t.Fatal("invalid YAML accepted")
	}
	// Returned integers and caller-owned path slices cannot modify shared nodes.
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 20; n++ {
				path := []string{"capacity"}
				got, err := Number("resources", "res-lsu-load-tags", path...)
				path[0] = "missing"
				if err != nil || got != want {
					t.Errorf("concurrent read: %d %v", got, err)
				}
			}
		}()
	}
	wg.Wait()
}

func TestNumberCachedLookupParity(t *testing.T) {
	for _, q := range []struct {
		collection, id string
		path           []string
	}{
		{"resources", "res-lsu-load-tags", []string{"capacity"}},
		{"boundaries", "b-dispatch", []string{"rtl_parameters", "SIZE"}},
		{"timing_measurements", "tm-buffer-0", []string{"value"}},
		{"resources", "res-lsu-load-tags", nil},
		{"resources", "res-lsu-load-tags", []string{"missing"}},
		{"resources", "missing", []string{"capacity"}},
		{"missing", "missing", nil},
	} {
		got, err := Number(q.collection, q.id, q.path...)
		want, wantErr := number(document, q.collection, q.id, q.path...)
		if got != want || fmt.Sprint(err) != fmt.Sprint(wantErr) {
			t.Fatalf("%+v: %d %v != %d %v", q, got, err, want, wantErr)
		}
	}
}

// The parse subcase is the previous Number implementation, using identical IR.
// This bounds the isolated lookup measurement independently of simulator work.
func BenchmarkNumberIR(b *testing.B) {
	for _, cached := range []bool{false, true} {
		b.Run(fmt.Sprintf("cached=%t", cached), func(b *testing.B) {
			if cached {
				if _, err := Number("resources", "res-lsu-load-tags", "capacity"); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				var n int
				var err error
				if cached {
					n, err = Number("resources", "res-lsu-load-tags", "capacity")
				} else {
					n, err = number(document, "resources", "res-lsu-load-tags", "capacity")
				}
				if err != nil || n != 8 {
					b.Fatalf("%d %v", n, err)
				}
			}
		})
	}
}

func TestBufferSharedIRIsolation(t *testing.T) {
	ir, err := bufferIR()
	if err != nil {
		t.Fatal(err)
	}
	// Check every boundary, including unsupported kinds, against fresh parsing.
	for _, boundary := range ir.Boundaries {
		got, err := Buffer(boundary.ID)
		want, wantErr := buffer(document, boundary.ID)
		if got != want || fmt.Sprint(err) != fmt.Sprint(wantErr) {
			t.Fatalf("%s: %+v %v != %+v %v", boundary.ID, got, err, want, wantErr)
		}
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 20; n++ {
				spec, err := Buffer("b-dispatch")
				if err != nil || spec.Size != 4 || spec.OutReg != 1 {
					t.Errorf("shared buffer changed: %+v %v", spec, err)
				}
				spec.Size = 128 // Returned configuration is an independent value.
			}
		}()
	}
	wg.Wait()
	if _, err := buffer([]byte("["), "b-dispatch"); err == nil {
		t.Fatal("invalid YAML accepted")
	}
}

func BenchmarkBufferIR(b *testing.B) {
	for _, cached := range []bool{false, true} {
		b.Run(fmt.Sprintf("cached=%t", cached), func(b *testing.B) {
			if cached {
				if _, err := Buffer("b-dispatch"); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				var spec BufferSpec
				var err error
				if cached {
					spec, err = Buffer("b-dispatch")
				} else {
					spec, err = buffer(document, "b-dispatch")
				}
				if err != nil || spec.Size != 4 || spec.OutReg != 1 {
					b.Fatalf("%+v %v", spec, err)
				}
			}
		})
	}
}
