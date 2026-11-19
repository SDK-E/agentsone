// SPDX-License-Identifier: MIT

package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// GenesisHash is the previous hash of the first record: 64 zeros.
const GenesisHash = "0000000000000000000000000000000000000000000000000000000000000000"

// Domain errors.
var (
	ErrInvalidActor  = errors.New("invalid audit actor")
	ErrInvalidTarget = errors.New("invalid audit target")
	// ErrOutOfOrder is returned when a record does not continue the chain.
	ErrOutOfOrder = errors.New("audit record out of order")
)

// ActorKind is who acted.
type ActorKind string

// Actor kinds.
const (
	ActorAgent  ActorKind = "agent"
	ActorHuman  ActorKind = "human"
	ActorSystem ActorKind = "system"
	ActorHost   ActorKind = "host"
)

// Actor is who did something.
type Actor struct {
	Kind ActorKind
	Name string
	// UID is the Linux uid seen on the socket, nil when unknown.
	UID *int
}

// ParseActorRef reads "<kind>:<name>", for example "agent:founder".
func ParseActorRef(ref string) (Actor, error) {
	kind, name, ok := strings.Cut(ref, ":")
	if !ok || name == "" || !knownActorKind(ActorKind(kind)) {
		return Actor{}, fmt.Errorf("%w: %q", ErrInvalidActor, ref)
	}
	return Actor{Kind: ActorKind(kind), Name: name}, nil
}

func knownActorKind(k ActorKind) bool {
	switch k {
	case ActorAgent, ActorHuman, ActorSystem, ActorHost:
		return true
	}
	return false
}

// Target is what the action was done to.
type Target struct {
	Kind string // agent, plugin, machine, ...
	ID   string
}

// ParseTargetRef reads "<kind>:<id>". A bare kind such as "machine" names
// the one thing of that kind.
func ParseTargetRef(ref string) (Target, error) {
	if ref == "" {
		return Target{}, fmt.Errorf("%w: empty", ErrInvalidTarget)
	}
	kind, id, ok := strings.Cut(ref, ":")
	if !ok {
		return Target{Kind: ref, ID: ref}, nil
	}
	if kind == "" || id == "" {
		return Target{}, fmt.Errorf("%w: %q", ErrInvalidTarget, ref)
	}
	return Target{Kind: kind, ID: id}, nil
}

// Outcome says whether the action happened.
type Outcome string

// Outcomes.
const (
	OutcomeSuccess Outcome = "success"
	OutcomeFailure Outcome = "failure"
	OutcomeDenied  Outcome = "denied"
)

// Origin says who wrote the record: inside the VM (testimony) or the host (proof).
type Origin string

// Origins.
const (
	OriginInside Origin = "inside"
	OriginHost   Origin = "host"
)

// Record is one entry in the hash-chained audit log.
type Record struct {
	Seq     int64
	Time    Timestamp
	Actor   Actor
	Action  Action
	Target  Target
	Outcome Outcome
	// Details is a JSON object kept byte for byte, so hashes stay stable.
	Details  json.RawMessage
	Origin   Origin
	PrevHash string
	Hash     string
}

// Follows reports whether r continues the chain after last. hasLast is
// false for an empty log, where r must be the first record.
func (r Record) Follows(last Record, hasLast bool) bool {
	if !hasLast {
		return r.Seq == 1 && r.PrevHash == GenesisHash
	}
	return r.Seq == last.Seq+1 && r.PrevHash == last.Hash
}
