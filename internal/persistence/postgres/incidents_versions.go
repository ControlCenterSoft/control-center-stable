package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"control-center/internal/incidents"
)

const incidentResourceVersionEntropyBytes = 16

// IncidentResourceVersionGenerator owns opaque incident resource versions at
// the persistence boundary. Values carry no ordering semantics and contain no
// host, tenant, actor, timestamp, or provider information.
type IncidentResourceVersionGenerator struct {
	random io.Reader
}

func NewIncidentResourceVersionGenerator() *IncidentResourceVersionGenerator {
	return &IncidentResourceVersionGenerator{random: rand.Reader}
}

func newIncidentResourceVersionGenerator(random io.Reader) *IncidentResourceVersionGenerator {
	return &IncidentResourceVersionGenerator{random: random}
}

func (g *IncidentResourceVersionGenerator) NextIncidentResourceVersion(ctx context.Context, current incidents.Incident) (string, error) {
	if g == nil || g.random == nil {
		return "", incidents.ErrOperatorDependencyUnavailable
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := current.Validate(); err != nil {
		return "", fmt.Errorf("current incident is invalid: %w", err)
	}
	entropy := make([]byte, incidentResourceVersionEntropyBytes)
	if _, err := io.ReadFull(g.random, entropy); err != nil {
		return "", fmt.Errorf("generate incident resource version: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	version := "irv-" + hex.EncodeToString(entropy)
	if version == current.ResourceVersion {
		return "", errors.New("generated incident resource version collides with current version")
	}
	return version, nil
}

var _ incidents.ResourceVersionGenerator = (*IncidentResourceVersionGenerator)(nil)
