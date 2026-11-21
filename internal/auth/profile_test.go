package auth

import (
	"testing"

	utls "github.com/refraction-networking/utls"
)

func TestHTTP1OnlySpec_AdvertisesOnlyHTTP11(t *testing.T) {
	for _, id := range []utls.ClientHelloID{utls.HelloChrome_Auto, utls.HelloFirefox_Auto, utls.HelloEdge_Auto} {
		spec, err := http1OnlySpec(id)
		if err != nil {
			t.Fatalf("%s: %v", id.Str(), err)
		}
		found := false
		for _, ext := range spec.Extensions {
			if alpn, ok := ext.(*utls.ALPNExtension); ok {
				found = true
				if len(alpn.AlpnProtocols) != 1 || alpn.AlpnProtocols[0] != "http/1.1" {
					t.Errorf("%s: ALPN = %v, want [http/1.1]", id.Str(), alpn.AlpnProtocols)
				}
			}
		}
		if !found {
			t.Errorf("%s: no ALPN extension in spec", id.Str())
		}
	}
}
