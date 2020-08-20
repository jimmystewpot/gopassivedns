package main

import (
	"testing"

	"github.com/google/gopacket/layers"
)

func BenchmarkTypeString(b *testing.B) {
	type args struct {
		dnsType layers.DNSType
	}
	benchmarks := []struct {
		name string
		args args
		want string
	}{
		{
			name: "SRV",
			args: args{
				dnsType: layers.DNSTypeSRV,
			},
			want: "SRV",
		},
		{
			name: "A",
			args: args{
				dnsType: layers.DNSTypeA,
			},
			want: "A",
		},
		{
			name: "AAAA",
			args: args{
				dnsType: layers.DNSTypeAAAA,
			},
			want: "AAAA",
		},
		{
			name: "TXT",
			args: args{
				dnsType: layers.DNSTypeTXT,
			},
			want: "TXT",
		},
		{
			name: "SOA",
			args: args{
				dnsType: layers.DNSTypeSOA,
			},
			want: "SOA",
		},
		{
			name: "ANY",
			args: args{
				dnsType: 255,
			},
			want: "ANY",
		},
	}
	for _, bb := range benchmarks {
		b.Run(bb.name, func(b *testing.B) {
			for n := 0; n < b.N; n++ {
				if got := TypeString(bb.args.dnsType); got != bb.want {
					b.Errorf("TypeString() = %v, want %v", got, bb.want)
				}
			}
		})
	}
}

func TestTypeString(t *testing.T) {
	type args struct {
		dnsType layers.DNSType
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "SRV",
			args: args{
				dnsType: layers.DNSTypeSRV,
			},
			want: "SRV",
		},
		{
			name: "A",
			args: args{
				dnsType: layers.DNSTypeA,
			},
			want: "A",
		},
		{
			name: "AAAA",
			args: args{
				dnsType: layers.DNSTypeAAAA,
			},
			want: "AAAA",
		},
		{
			name: "TXT",
			args: args{
				dnsType: layers.DNSTypeTXT,
			},
			want: "TXT",
		},
		{
			name: "SOA",
			args: args{
				dnsType: layers.DNSTypeSOA,
			},
			want: "SOA",
		},
		{
			name: "ANY",
			args: args{
				dnsType: 255,
			},
			want: "ANY",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := TypeString(tt.args.dnsType); got != tt.want {
				t.Errorf("TypeString() = %v, want %v", got, tt.want)
			}
		})
	}
}
