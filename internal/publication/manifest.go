package publication

import (
	"errors"
	"regexp"
)

var (
	ErrInvalidManifest = errors.New("invalid release manifest")
	versionPattern     = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	commitPattern      = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
)

type Manifest struct {
	Product string
	Version string
	Channel string
	Commit  string
}

func Validate(manifest Manifest) error {
	if manifest.Product == "" || !versionPattern.MatchString(manifest.Version) || !commitPattern.MatchString(manifest.Commit) {
		return ErrInvalidManifest
	}
	switch manifest.Channel {
	case "development", "candidate", "stable":
		return nil
	default:
		return ErrInvalidManifest
	}
}

func CanonicalID(manifest Manifest) (string, error) {
	if err := Validate(manifest); err != nil {
		return "", err
	}
	return manifest.Product + "@" + manifest.Version + ":" + manifest.Channel, nil
}
