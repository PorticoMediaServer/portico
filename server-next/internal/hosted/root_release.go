//go:build release

package hosted

func init() {
	if _, err := linkedRoot(); err != nil {
		panic("release requires the official Portico Services root public key at build time")
	}
}
