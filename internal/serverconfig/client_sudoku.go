package serverconfig

// buildSudokuMihomoYAML emits a single-line flow-style YAML proxy map. This
// preserves Mihomo's native Sudoku keys while keeping the value copy-friendly
// in the client access view.
func buildSudokuMihomoYAML(input Input, address, fragment string) (string, error) {
	proxy := map[string]any{
		"name": fragment, "type": "sudoku", "server": address, "port": input.Port, "key": input.SudokuClientKey,
		"aead-method": input.Method, "padding-min": input.SudokuPaddingMin, "padding-max": input.SudokuPaddingMax,
		"table-type": input.SudokuTableType, "enable-pure-downlink": input.SudokuEnablePureDownlink,
		"multiplex": input.SudokuMultiplex,
		"httpmask": compactMap(map[string]any{
			"disable": !input.SudokuHTTPMaskEnabled, "mode": input.SudokuHTTPMaskMode,
			"tls": input.SudokuHTTPMaskTLS, "host": input.SudokuHTTPMaskHost,
			"path-root": input.SudokuHTTPMaskPathRoot, "multiplex": input.SudokuMultiplex,
		}),
	}
	return marshalSingleLineYAML(proxy)
}
