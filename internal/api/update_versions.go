package api

import (
	"regexp"
	"strconv"
	"strings"
)

var releaseVersionPattern = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:\+.*)?$`)

var commitVersionPattern = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

func releaseVersion(value string) ([3]int, bool) {
	match := releaseVersionPattern.FindStringSubmatch(strings.TrimSpace(value))
	if len(match) != 4 {
		return [3]int{}, false
	}
	var parsed [3]int
	for index := range parsed {
		part, err := strconv.Atoi(match[index+1])
		if err != nil {
			return [3]int{}, false
		}
		parsed[index] = part
	}
	return parsed, true
}

func versionOlder(current, latest string) (bool, bool) {
	left, leftOK := releaseVersion(current)
	right, rightOK := releaseVersion(latest)
	if !leftOK || !rightOK {
		return false, false
	}
	for index := range left {
		if left[index] != right[index] {
			return left[index] < right[index], true
		}
	}
	return false, true
}

func imageVersionStatus(current, latest string) (bool, bool) {
	current = strings.ToLower(strings.TrimSpace(current))
	latest = strings.ToLower(strings.TrimSpace(latest))
	if commitVersionPattern.MatchString(current) && commitVersionPattern.MatchString(latest) {
		matches := strings.HasPrefix(current, latest) || strings.HasPrefix(latest, current)
		return !matches, true
	}
	return versionOlder(current, latest)
}
