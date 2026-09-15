package api

func agentHasFeature(features []string, feature string) bool {
	for _, candidate := range features {
		if candidate == feature {
			return true
		}
	}
	return false
}
