package openai

import (
	"strings"
	"testing"
	"time"
)

func TestOptionsDefaults(t *testing.T) {
	d := Defaults()
	if d.Enabled {
		t.Error("Enabled should default to false")
	}
	if d.MaxRuns != 256 {
		t.Errorf("MaxRuns = %d, want 256", d.MaxRuns)
	}
	if d.MaxRunsPerTenant != 32 {
		t.Errorf("MaxRunsPerTenant = %d, want 32", d.MaxRunsPerTenant)
	}
	if d.RPSPerTenant != 10 {
		t.Errorf("RPSPerTenant = %d, want 10", d.RPSPerTenant)
	}
	if d.RingSize != 512 {
		t.Errorf("RingSize = %d, want 512", d.RingSize)
	}
	if d.ExpiresAfterSeconds != 600 {
		t.Errorf("ExpiresAfterSeconds = %d, want 600", d.ExpiresAfterSeconds)
	}
	if d.MaxRequestBodyBytes != 10*1024*1024 {
		t.Errorf("MaxRequestBodyBytes = %d, want 10MiB", d.MaxRequestBodyBytes)
	}
	if d.ErrorDetailMode != ErrorDetailDev {
		t.Errorf("ErrorDetailMode = %q, want dev", d.ErrorDetailMode)
	}
}

func TestOptionsValidate_FillsDefaults(t *testing.T) {
	o := Options{}
	if err := o.Validate(); err != nil {
		t.Fatalf("zero Options should validate after default fill: %v", err)
	}
	d := Defaults()
	if o.MaxRuns != d.MaxRuns {
		t.Errorf("MaxRuns not filled: %d", o.MaxRuns)
	}
	if o.MaxRunsPerTenant != d.MaxRunsPerTenant {
		t.Errorf("MaxRunsPerTenant not filled: %d", o.MaxRunsPerTenant)
	}
	if o.RPSPerTenant != d.RPSPerTenant {
		t.Errorf("RPSPerTenant not filled: %d", o.RPSPerTenant)
	}
	if o.RingSize != d.RingSize {
		t.Errorf("RingSize not filled: %d", o.RingSize)
	}
	if o.ExpiresAfterSeconds != d.ExpiresAfterSeconds {
		t.Errorf("ExpiresAfterSeconds not filled: %d", o.ExpiresAfterSeconds)
	}
	if o.MaxRequestBodyBytes != d.MaxRequestBodyBytes {
		t.Errorf("MaxRequestBodyBytes not filled: %d", o.MaxRequestBodyBytes)
	}
	if o.ErrorDetailMode != d.ErrorDetailMode {
		t.Errorf("ErrorDetailMode not filled: %q", o.ErrorDetailMode)
	}
}

func TestOptionsValidate_Errors(t *testing.T) {
	cases := []struct {
		name string
		o    Options
		want string
	}{
		{"negative max_runs_per_tenant", Options{MaxRunsPerTenant: -1}, "MaxRunsPerTenant"},
		{"negative rps", Options{RPSPerTenant: -1}, "RPSPerTenant"},
		{"expires too low", Options{ExpiresAfterSeconds: 30}, "ExpiresAfterSeconds"},
		{"expires too high", Options{ExpiresAfterSeconds: 90000}, "ExpiresAfterSeconds"},
		{"negative body bytes", Options{MaxRequestBodyBytes: -1}, "MaxRequestBodyBytes"},
		{"unknown error mode", Options{ErrorDetailMode: "verbose"}, "ErrorDetailMode"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.o.Validate()
			if err == nil {
				t.Fatalf("want error, got nil")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %q, want substring %q", err.Error(), c.want)
			}
		})
	}
}

func TestOptionsValidate_AcceptsBothErrorModes(t *testing.T) {
	for _, mode := range []string{ErrorDetailDev, ErrorDetailProd} {
		o := Options{ErrorDetailMode: mode}
		if err := o.Validate(); err != nil {
			t.Errorf("mode %q: unexpected err %v", mode, err)
		}
		if o.ErrorDetailMode != mode {
			t.Errorf("mode mutated: got %q", o.ErrorDetailMode)
		}
	}
}

func TestOptionsExpiresAfter(t *testing.T) {
	o := Options{ExpiresAfterSeconds: 120}
	if got, want := o.ExpiresAfter(), 2*time.Minute; got != want {
		t.Errorf("ExpiresAfter = %s, want %s", got, want)
	}
}
