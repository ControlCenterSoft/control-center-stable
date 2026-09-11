package ui

import "sort"

type NavigationItem struct {
	ID    string
	Label string
	Order int
}

var navigationCatalog = []NavigationItem{
	{ID: "overview", Label: "Overview", Order: 10},
	{ID: "nodes", Label: "Nodes", Order: 20},
	{ID: "inventory", Label: "Inventory", Order: 30},
	{ID: "automation", Label: "Automation", Order: 40},
	{ID: "pxe", Label: "PXE", Order: 50},
	{ID: "domain", Label: "Domain", Order: 60},
	{ID: "market", Label: "Market", Order: 70},
}

func Navigation(enabled map[string]bool) []NavigationItem {
	items := make([]NavigationItem, 0, len(navigationCatalog))
	for _, item := range navigationCatalog {
		if item.ID == "overview" || enabled[item.ID] {
			items = append(items, item)
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Order == items[j].Order {
			return items[i].ID < items[j].ID
		}
		return items[i].Order < items[j].Order
	})
	return items
}
