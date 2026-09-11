package ui

import "sort"

type OverviewInput struct {
	DomainProvider string
	DomainReady    bool
	InventoryCount int
	AgentCount     int
	AgentsOnline   int
	AgentsDelayed  int
	AgentsOffline  int
}

type OverviewCard struct {
	ID     string         `json:"id"`
	Status string         `json:"status"`
	Values map[string]any `json:"values"`
}

type Overview struct {
	Status string         `json:"status"`
	Cards  []OverviewCard `json:"cards"`
}

func BuildOverview(input OverviewInput) Overview {
	status := "ready"
	if !input.DomainReady || input.AgentsOffline > 0 {
		status = "degraded"
	} else if input.AgentsDelayed > 0 {
		status = "attention"
	}
	cards := []OverviewCard{
		{ID: "domain", Status: readyStatus(input.DomainReady), Values: map[string]any{"provider": input.DomainProvider}},
		{ID: "inventory", Status: "ready", Values: map[string]any{"devices": max(0, input.InventoryCount)}},
		{ID: "agents", Status: agentStatus(input), Values: map[string]any{
			"nodes": max(0, input.AgentCount), "online": max(0, input.AgentsOnline),
			"delayed": max(0, input.AgentsDelayed), "offline": max(0, input.AgentsOffline),
		}},
	}
	sort.Slice(cards, func(i, j int) bool { return cards[i].ID < cards[j].ID })
	return Overview{Status: status, Cards: cards}
}

func readyStatus(ready bool) string {
	if ready {
		return "ready"
	}
	return "blocked"
}

func agentStatus(input OverviewInput) string {
	if input.AgentsOffline > 0 {
		return "degraded"
	}
	if input.AgentsDelayed > 0 {
		return "attention"
	}
	return "ready"
}
