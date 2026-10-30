//go:build linux

package decoder

func helperSpec(arg string) (string, []string, error) {
	spec, err := decodeLinux(arg)
	return spec.Executable, spec.Arguments, err
}
