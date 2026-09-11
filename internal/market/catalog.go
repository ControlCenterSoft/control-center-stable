package market

import (
	"errors"
	"sort"
	"strings"
	"sync"
)

var (
	ErrInvalidModule = errors.New("invalid module")
	ErrDuplicate     = errors.New("module already registered")
)

type Module struct {
	ID          string
	Version     string
	DisplayName string
	Category    string
}

type Catalog struct {
	mu      sync.RWMutex
	modules map[string]Module
}

func NewCatalog() *Catalog {
	return &Catalog{modules: make(map[string]Module)}
}

func (c *Catalog) Register(module Module) error {
	module.ID = strings.TrimSpace(module.ID)
	module.Version = strings.TrimSpace(module.Version)
	module.DisplayName = strings.TrimSpace(module.DisplayName)
	if module.ID == "" || module.Version == "" || module.DisplayName == "" {
		return ErrInvalidModule
	}

	key := module.ID + "@" + module.Version
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.modules[key]; exists {
		return ErrDuplicate
	}
	c.modules[key] = module
	return nil
}

func (c *Catalog) List() []Module {
	c.mu.RLock()
	modules := make([]Module, 0, len(c.modules))
	for _, module := range c.modules {
		modules = append(modules, module)
	}
	c.mu.RUnlock()

	sort.Slice(modules, func(i, j int) bool {
		if modules[i].ID == modules[j].ID {
			return modules[i].Version < modules[j].Version
		}
		return modules[i].ID < modules[j].ID
	})
	return modules
}

func (c *Catalog) Find(id, version string) (Module, bool) {
	key := strings.TrimSpace(id) + "@" + strings.TrimSpace(version)
	c.mu.RLock()
	module, ok := c.modules[key]
	c.mu.RUnlock()
	return module, ok
}
