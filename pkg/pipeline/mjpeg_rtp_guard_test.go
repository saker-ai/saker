package pipeline

import "testing"

func TestIsValidMJPEGRTP(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		payload []byte
		want    bool
	}{
		{name: "nil payload", payload: nil, want: false},
		{name: "empty payload", payload: []byte{}, want: false},
		{name: "too short for header", payload: make([]byte, 7), want: false},

		// Normal type (t < 64), q < 128: 8-byte header, no quant tables needed.
		{
			name:    "normal type q<128 minimal",
			payload: make([]byte, 8),
			want:    true,
		},

		// Normal type, q >= 128: need 8 + 132 = 140 bytes minimum.
		{
			name: "normal type q>=128 too short",
			payload: func() []byte {
				b := make([]byte, 139)
				b[5] = 128 // q = 128
				return b
			}(),
			want: false,
		},
		{
			name: "normal type q>=128 exact minimum",
			payload: func() []byte {
				b := make([]byte, 140)
				b[5] = 128
				return b
			}(),
			want: true,
		},
		{
			name: "normal type q>=128 large payload",
			payload: func() []byte {
				b := make([]byte, 1400)
				b[5] = 200
				return b
			}(),
			want: true,
		},

		// Restart marker type (64 <= t <= 127): 12-byte header.
		{
			name: "restart type q<128 too short",
			payload: func() []byte {
				b := make([]byte, 11)
				b[4] = 64 // restart marker type
				return b
			}(),
			want: false,
		},
		{
			name: "restart type q<128 ok",
			payload: func() []byte {
				b := make([]byte, 12)
				b[4] = 64
				return b
			}(),
			want: true,
		},
		{
			name: "restart type q>=128 too short",
			payload: func() []byte {
				b := make([]byte, 143)
				b[4] = 100 // restart type
				b[5] = 255 // q = 255
				return b
			}(),
			want: false,
		},
		{
			name: "restart type q>=128 exact minimum",
			payload: func() []byte {
				b := make([]byte, 144)
				b[4] = 100
				b[5] = 255
				return b
			}(),
			want: true,
		},

		// Reproduce the exact crash: capacity 16, after 8-byte skip leaves
		// only 8 bytes, but RTPDepay tries b[4:68].
		{
			name: "crash reproduction: cap 16 q>=128",
			payload: func() []byte {
				b := make([]byte, 16)
				b[5] = 128
				return b
			}(),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isValidMJPEGRTP(tt.payload); got != tt.want {
				t.Errorf("isValidMJPEGRTP(%d bytes, t=%d, q=%d) = %v, want %v",
					len(tt.payload), safeIndex(tt.payload, 4), safeIndex(tt.payload, 5), got, tt.want)
			}
		})
	}
}

func safeIndex(b []byte, i int) byte {
	if i < len(b) {
		return b[i]
	}
	return 0
}
