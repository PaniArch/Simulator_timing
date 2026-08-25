package softfloat

import (
	"sync"
	"testing"
)

func requireResult(t *testing.T, got Result32, bits uint32, flags ExceptionFlags) {
	t.Helper()
	if got.Bits != bits || got.Flags != flags {
		t.Fatalf("got {Bits:%#08x Flags:%#02x}, want {Bits:%#08x Flags:%#02x}",
			got.Bits, got.Flags, bits, flags)
	}
}

func TestF32Arithmetic(t *testing.T) {
	tests := []struct {
		name string
		run  func() (Result32, error)
		want uint32
	}{
		{"add", func() (Result32, error) { return F32Add(0x3fc00000, 0x40100000, RoundNearEven) }, 0x40700000},
		{"sub", func() (Result32, error) { return F32Sub(0x40700000, 0x40100000, RoundNearEven) }, 0x3fc00000},
		{"mul", func() (Result32, error) { return F32Mul(0x3fc00000, 0x40000000, RoundNearEven) }, 0x40400000},
		{"div", func() (Result32, error) { return F32Div(0x40400000, 0x40000000, RoundNearEven) }, 0x3fc00000},
		{"sqrt", func() (Result32, error) { return F32Sqrt(0x40800000, RoundNearEven) }, 0x40000000},
		{"fma", func() (Result32, error) { return F32FMA(0x3fc00000, 0x40000000, 0x3f000000, RoundNearEven) }, 0x40600000},
		{"fma-fused", func() (Result32, error) { return F32FMA(0x3f800001, 0x3f7ffffe, 0xbf800000, RoundNearEven) }, 0xa8800000},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := test.run()
			if err != nil {
				t.Fatal(err)
			}
			requireResult(t, got, test.want, 0)
		})
	}
}

func TestF32RoundingAndFlags(t *testing.T) {
	nearest, err := F32Add(0x3f800000, 0x33800000, RoundNearEven)
	if err != nil {
		t.Fatal(err)
	}
	requireResult(t, nearest, 0x3f800000, FlagInexact)

	up, err := F32Add(0x3f800000, 0x33800000, RoundUp)
	if err != nil {
		t.Fatal(err)
	}
	requireResult(t, up, 0x3f800001, FlagInexact)

	divZero, err := F32Div(0x3f800000, 0x00000000, RoundNearEven)
	if err != nil {
		t.Fatal(err)
	}
	requireResult(t, divZero, 0x7f800000, FlagDivideByZero)

	invalid, err := F32Sqrt(0xbf800000, RoundNearEven)
	if err != nil {
		t.Fatal(err)
	}
	requireResult(t, invalid, 0x7fc00000, FlagInvalid)

	overflow, err := F32Mul(0x7f7fffff, 0x40000000, RoundNearEven)
	if err != nil {
		t.Fatal(err)
	}
	requireResult(t, overflow, 0x7f800000, FlagOverflow|FlagInexact)

	underflow, err := F32Mul(0x00800000, 0x00800000, RoundNearEven)
	if err != nil {
		t.Fatal(err)
	}
	requireResult(t, underflow, 0x00000000, FlagUnderflow|FlagInexact)
}

func TestF32Conversions(t *testing.T) {
	fromSigned, err := I32ToF32(0xfffffffe, RoundNearEven)
	if err != nil {
		t.Fatal(err)
	}
	requireResult(t, fromSigned, 0xc0000000, 0)

	fromUnsigned, err := UI32ToF32(3, RoundNearEven)
	if err != nil {
		t.Fatal(err)
	}
	requireResult(t, fromUnsigned, 0x40400000, 0)

	toSigned, err := F32ToI32(0x3fc00000, RoundNearEven)
	if err != nil {
		t.Fatal(err)
	}
	requireResult(t, toSigned, 2, FlagInexact)

	toUnsigned, err := F32ToUI32(0x3fc00000, RoundTowardZero)
	if err != nil {
		t.Fatal(err)
	}
	requireResult(t, toUnsigned, 1, FlagInexact)
}

func TestF32Compare(t *testing.T) {
	if got := F32Equal(0x3f800000, 0x3f800000); !got.Value || got.Flags != 0 {
		t.Fatalf("equal: %+v", got)
	}
	if got := F32LessThan(0x3f800000, 0x40000000); !got.Value || got.Flags != 0 {
		t.Fatalf("less than: %+v", got)
	}
	if got := F32LessOrEqual(0x40000000, 0x3f800000); got.Value || got.Flags != 0 {
		t.Fatalf("less or equal: %+v", got)
	}
	if got := F32LessThan(0x7fc00000, 0x3f800000); got.Value || got.Flags != FlagInvalid {
		t.Fatalf("NaN comparison: %+v", got)
	}
	if got := F32Equal(0x7fc00000, 0x3f800000); got.Value || got.Flags != 0 {
		t.Fatalf("quiet NaN equality: %+v", got)
	}
}

func TestInvalidRoundingMode(t *testing.T) {
	if _, err := F32Add(0, 0, RoundingMode(5)); err == nil {
		t.Fatal("expected unsupported rounding mode error")
	}
}

func TestConcurrentCallsAreIsolated(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				got, err := F32Add(0x3f800000, 0x33800000, RoundUp)
				if err != nil || got.Bits != 0x3f800001 || got.Flags != FlagInexact {
					t.Errorf("got %+v, err %v", got, err)
					return
				}
			}
		}()
	}
	wg.Wait()
}
