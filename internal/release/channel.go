package release

import "errors"

type Channel string

const (
	ChannelDevelopment Channel = "development"
	ChannelCandidate   Channel = "candidate"
	ChannelStable      Channel = "stable"
)

var ErrInvalidPromotion = errors.New("invalid release channel promotion")

func CanPromote(from, to Channel) bool {
	if from == to {
		return true
	}
	switch from {
	case ChannelDevelopment:
		return to == ChannelCandidate
	case ChannelCandidate:
		return to == ChannelStable
	case ChannelStable:
		return false
	default:
		return false
	}
}

func ValidatePromotion(from, to Channel) error {
	if !validChannel(from) || !validChannel(to) || !CanPromote(from, to) {
		return ErrInvalidPromotion
	}
	return nil
}

func validChannel(channel Channel) bool {
	return channel == ChannelDevelopment || channel == ChannelCandidate || channel == ChannelStable
}
