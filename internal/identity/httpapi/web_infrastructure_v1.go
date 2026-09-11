package httpapi

import (
	"fmt"
	"html/template"
	"io"
	"time"

	productui "control-center/internal/ui"
)

type infrastructureInventoryPageData struct {
	Version              string
	DisplayName          string
	Username             string
	Inventory            productui.InfrastructureInventory
	InventoryUnavailable bool
}

var infrastructureInventoryTemplate = template.Must(template.New("infrastructure-v1").Funcs(template.FuncMap{
	"stateLabel": inventoryStateLabel,
	"bytes":      formatInventoryBytes,
	"observed":   formatInventoryObservedAt,
}).Parse(`<!doctype html>
<html lang="ru" data-locale="ru-RU">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Control Center — Инфраструктура</title>
<style>
:root{font-family:system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;color-scheme:light dark;--s1:.35rem;--s2:.65rem;--s3:1rem;--s4:1.5rem;--radius:.8rem;--border:1px solid currentColor}
*{box-sizing:border-box}body{margin:0;min-height:100vh;background:Canvas;color:CanvasText}a{color:inherit}.skip{position:absolute;left:-9999px}.skip:focus{left:var(--s3);top:var(--s3);z-index:10;background:Canvas;padding:var(--s2);border:var(--border)}.topbar{display:flex;gap:var(--s3);align-items:center;justify-content:space-between;padding:var(--s3) var(--s4);border-bottom:var(--border)}.brand{display:grid;gap:var(--s1)}.muted,.eyebrow{font-size:.85rem;opacity:.78}.header-meta,.row{display:flex;gap:var(--s2);align-items:center;flex-wrap:wrap}.badge{display:inline-flex;gap:.4rem;align-items:center;border:var(--border);border-radius:999px;padding:.35rem .65rem;font-size:.85rem}.shell{display:grid;grid-template-columns:15rem minmax(0,1fr);min-height:calc(100vh - 5rem)}.sidebar{padding:var(--s4);border-right:var(--border)}.sidebar nav{display:grid;gap:var(--s2)}.nav{display:block;padding:.65rem .75rem;border-radius:var(--radius);text-decoration:none}.nav-current{border:var(--border);font-weight:700}.nav-disabled{opacity:.65}.content{padding:var(--s4);max-width:86rem;width:100%;margin:0 auto}.page-head{display:flex;gap:var(--s3);align-items:flex-start;justify-content:space-between;margin-bottom:var(--s4)}.page-head h1,.site h2,.node h3{margin-top:0}.notice,.site,.node{border:var(--border);border-radius:var(--radius);padding:var(--s3)}.sites{display:grid;gap:var(--s3)}.nodes{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:var(--s3);margin-top:var(--s3)}.node{min-width:0}.facts{display:grid;grid-template-columns:max-content 1fr;gap:var(--s2) var(--s3);margin:0}.facts dt{font-weight:700}.facts dd{margin:0;overflow-wrap:anywhere}.evidence{margin-top:var(--s3);padding-top:var(--s2);border-top:var(--border)}.footer{margin-top:var(--s4);padding-top:var(--s3);border-top:var(--border);display:flex;gap:var(--s3);align-items:center;justify-content:space-between;flex-wrap:wrap}button{font:inherit;padding:.55rem .8rem;border:var(--border);border-radius:.55rem;background:Canvas;color:CanvasText;cursor:pointer}@media(max-width:780px){.shell{grid-template-columns:1fr}.sidebar{border-right:0;border-bottom:var(--border)}.sidebar nav{grid-template-columns:repeat(3,minmax(0,1fr))}.nodes{grid-template-columns:1fr}.topbar,.page-head{align-items:flex-start;flex-direction:column}.content,.sidebar{padding:var(--s3)}}
</style>
</head>
<body>
<a class="skip" href="#main-content">К основному содержимому</a>
<header class="topbar">
<div class="brand"><span class="eyebrow">Control Center</span><strong>Инфраструктура</strong></div>
<div class="header-meta"><span class="badge">Версия {{.Version}}</span><span class="badge">Пользователь: {{.DisplayName}}</span></div>
</header>
<div class="shell">
<aside class="sidebar"><nav aria-label="Основная навигация"><a class="nav" href="/overview">Обзор</a><span class="nav nav-current" aria-current="page">Инфраструктура</span><span class="nav nav-disabled" aria-disabled="true">Настройки</span></nav></aside>
<main class="content" id="main-content">
<div class="page-head"><div><h1>Сайты и узлы</h1><p class="muted">Read-only представление фактического инвентаря. Неподтверждённые данные не считаются здоровыми или актуальными.</p></div>{{if not .InventoryUnavailable}}<span class="badge">Актуальность: {{stateLabel .Inventory.State}}</span>{{end}}</div>
{{if .InventoryUnavailable}}
<section class="notice" role="status"><strong>Инвентарь недоступен</strong><p>Один или несколько authoritative-источников ещё не подключены. Интерфейс не подменяет отсутствие данных нулевым количеством сайтов или узлов.</p></section>
{{else}}
<div class="row" aria-label="Сводка инвентаря"><span class="badge">Сайтов: {{.Inventory.SiteCount}}</span><span class="badge">Узлов: {{.Inventory.NodeCount}}</span><span class="badge">Состояние evidence: {{stateLabel .Inventory.State}}</span></div>
<div class="sites">
{{range .Inventory.Sites}}
<section class="site" aria-labelledby="site-{{.ID}}"><div class="row"><h2 id="site-{{.ID}}">{{.Name}}</h2><span class="badge">{{.NodeCount}} узл.</span></div><p class="muted">Site ID: {{.ID}} · Scope: {{.ScopeID}}</p>
{{if .Nodes}}<div class="nodes">{{range .Nodes}}
<article class="node"><div class="row"><h3>{{.Hostname}}</h3><span class="badge">{{stateLabel .Freshness}}</span></div><dl class="facts"><dt>Node ID</dt><dd>{{.ID}}</dd><dt>Роли</dt><dd>{{range $i,$role := .Roles}}{{if $i}}, {{end}}{{$role}}{{else}}нет данных{{end}}</dd><dt>Возможности</dt><dd>{{range $i,$cap := .Capabilities}}{{if $i}}, {{end}}{{$cap}}{{else}}нет данных{{end}}</dd><dt>CPU</dt><dd>{{.Hardware.CPUModel}} · {{.Hardware.LogicalCores}} логических ядер</dd><dt>Память</dt><dd>{{bytes .Hardware.MemoryBytes}}</dd><dt>Хранилище</dt><dd>{{bytes .Hardware.StorageBytes}} · устройств: {{.Hardware.StorageDevices}}</dd><dt>Сеть</dt><dd>интерфейсов: {{.Connectivity.Interfaces}}, up: {{.Connectivity.Up}}, down: {{.Connectivity.Down}}, unknown: {{.Connectivity.Unknown}}</dd><dt>Последнее наблюдение</dt><dd>{{observed .CollectedAt}}</dd></dl><div class="evidence"><strong>Границы evidence</strong><p class="muted">Desired State: {{.DesiredState}} · Actual State: {{.ActualState}} · Version skew: {{.VersionSkew}}</p></div></article>
{{end}}</div>{{else}}<p class="muted">Подтверждённых узлов для этого сайта нет.</p>{{end}}
</section>
{{else}}<section class="notice" role="status"><strong>Список сайтов пуст</strong><p>Источники загружены и вернули пустой подтверждённый набор.</p></section>{{end}}
</div>
{{end}}
<footer class="footer"><span class="muted">Учётная запись: {{.Username}} · Язык: русский (ru-RU)</span><form method="post" action="/web/logout"><button type="submit">Выйти</button></form></footer>
</main>
</div>
</body>
</html>`))

func renderInfrastructureInventory(w io.Writer, version, displayName, username string, inventory productui.InfrastructureInventory) error {
	data := infrastructureInventoryPageData{
		Version:              version,
		DisplayName:          displayName,
		Username:             username,
		Inventory:            inventory,
		InventoryUnavailable: inventory.State == productui.InventoryViewUnavailable,
	}
	return infrastructureInventoryTemplate.Execute(w, data)
}

func inventoryStateLabel(state productui.InventoryViewState) string {
	switch state {
	case productui.InventoryViewCurrent:
		return "актуально"
	case productui.InventoryViewStale:
		return "устарело"
	case productui.InventoryViewExpired:
		return "просрочено"
	default:
		return "нет подтверждённых данных"
	}
}

func formatInventoryObservedAt(value time.Time) string {
	if value.IsZero() {
		return "нет подтверждённых данных"
	}
	return value.UTC().Format(time.RFC3339)
}

func formatInventoryBytes(value uint64) string {
	const (
		kiB = uint64(1024)
		miB = 1024 * kiB
		giB = 1024 * miB
		tiB = 1024 * giB
	)
	switch {
	case value >= tiB:
		return fmt.Sprintf("%.2f TiB", float64(value)/float64(tiB))
	case value >= giB:
		return fmt.Sprintf("%.2f GiB", float64(value)/float64(giB))
	case value >= miB:
		return fmt.Sprintf("%.2f MiB", float64(value)/float64(miB))
	case value >= kiB:
		return fmt.Sprintf("%.2f KiB", float64(value)/float64(kiB))
	default:
		return fmt.Sprintf("%d B", value)
	}
}
