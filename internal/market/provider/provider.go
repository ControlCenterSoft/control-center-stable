package provider

import (
	"errors"
	"sort"
)

var ErrNoProvider = errors.New("no compatible provider")

type Provider struct {
	ID           string
	Platforms    []string
	Capabilities []string
	Priority     int
}

func Select(providers []Provider, platform, capability string) (Provider, error) {
	matches := make([]Provider, 0)
	for _, candidate := range providers {
		if candidate.ID == "" || !contains(candidate.Platforms, platform) || !contains(candidate.Capabilities, capability) {
			continue
		}
		matches = append(matches, candidate)
	}
	if len(matches) == 0 {
		return Provider{}, ErrNoProvider
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Priority == matches[j].Priority {
			return matches[i].ID < matches[j].ID
		}
		return matches[i].Priority > matches[j].Priority
	})
	return matches[0], nil
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
