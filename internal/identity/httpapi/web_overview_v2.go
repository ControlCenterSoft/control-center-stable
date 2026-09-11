package httpapi

import "html/template"

// productOverviewV2Template is the first Product Web Shell slice for 0.29.
// It is deliberately fail-closed about operational state: until a backend
// supplies context, freshness, risk and notifications, the UI says that the
// data is unavailable instead of inventing a healthy state.
var productOverviewV2Template = template.Must(template.New("overview-v2").Parse(`<!doctype html>
<html lang="ru" data-locale="ru-RU">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Control Center — Обзор</title>
<style>
:root{font-family:system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;color-scheme:light dark;--space-1:.35rem;--space-2:.65rem;--space-3:1rem;--space-4:1.5rem;--radius:.8rem;--border:1px solid currentColor}
*{box-sizing:border-box}body{margin:0;min-height:100vh;background:Canvas;color:CanvasText}a{color:inherit}.skip{position:absolute;left:-9999px}.skip:focus{left:var(--space-3);top:var(--space-3);z-index:10;background:Canvas;padding:var(--space-2);border:var(--border)}.topbar{display:flex;gap:var(--space-3);align-items:center;justify-content:space-between;padding:var(--space-3) var(--space-4);border-bottom:var(--border)}.brand{display:grid;gap:var(--space-1)}.eyebrow,.muted{font-size:.85rem;opacity:.78}.header-meta,.actions{display:flex;gap:var(--space-2);align-items:center;flex-wrap:wrap}.badge{display:inline-flex;gap:.4rem;align-items:center;border:var(--border);border-radius:999px;padding:.35rem .65rem;font-size:.85rem}.shell{display:grid;grid-template-columns:15rem minmax(0,1fr);min-height:calc(100vh - 5rem)}.sidebar{padding:var(--space-4);border-right:var(--border)}.sidebar nav{display:grid;gap:var(--space-2)}.nav-current,.nav-disabled{padding:.65rem .75rem;border-radius:var(--radius)}.nav-current{border:var(--border);font-weight:700}.nav-disabled{opacity:.65}.content{padding:var(--space-4);max-width:78rem;width:100%;margin:0 auto}.page-head{display:flex;gap:var(--space-3);align-items:flex-start;justify-content:space-between;margin-bottom:var(--space-4)}.page-head h1,.card h2,.context-panel h2{margin-top:0}.context-panel,.card{border:var(--border);border-radius:var(--radius);padding:var(--space-3)}.context-grid,.cards{display:grid;gap:var(--space-3)}.context-grid{grid-template-columns:repeat(3,minmax(0,1fr))}.context{display:grid;gap:var(--space-1);text-align:left;padding:var(--space-3);border:var(--border);border-radius:var(--radius);background:Canvas;color:CanvasText}.context[disabled]{opacity:1;cursor:not-allowed}.context strong{font-size:1rem}.cards{grid-template-columns:repeat(2,minmax(0,1fr));margin-top:var(--space-3)}.status-line{display:flex;gap:var(--space-2);align-items:flex-start}.status-icon{font-weight:800;min-width:1.25rem}.status-line strong,.status-line small{display:block}.status-line small{margin-top:var(--space-1);opacity:.78}.definition{display:grid;grid-template-columns:max-content 1fr;gap:var(--space-2) var(--space-3);margin:0}.definition dt{font-weight:700}.definition dd{margin:0}.footer{margin-top:var(--space-4);padding-top:var(--space-3);border-top:var(--border);display:flex;gap:var(--space-3);align-items:center;justify-content:space-between;flex-wrap:wrap}button{font:inherit;padding:.55rem .8rem;border:var(--border);border-radius:.55rem;background:Canvas;color:CanvasText;cursor:pointer}@media(max-width:780px){.shell{grid-template-columns:1fr}.sidebar{border-right:0;border-bottom:var(--border)}.sidebar nav{grid-template-columns:repeat(3,minmax(0,1fr))}.context-grid,.cards{grid-template-columns:1fr}.topbar,.page-head{align-items:flex-start;flex-direction:column}.content,.sidebar{padding:var(--space-3)}}
</style>
</head>
<body>
<a class="skip" href="#main-content" data-i18n="a11y.skip_to_content">К основному содержимому</a>
<header class="topbar">
<div class="brand"><span class="eyebrow" data-i18n="shell.product">Control Center</span><strong data-i18n="shell.section.overview">Обзор</strong></div>
<div class="header-meta" aria-label="Сведения о среде">
<span class="badge" data-i18n="shell.release"><span aria-hidden="true">●</span> Версия {{.Version}}</span>
<span class="badge" aria-label="Среда не определена" data-i18n="shell.environment.unknown"><span aria-hidden="true">◇</span> Среда: не определена</span>
</div>
</header>
<div class="shell">
<aside class="sidebar">
<nav aria-label="Основная навигация">
<span class="nav-current" aria-current="page" data-i18n="nav.overview">Обзор</span>
<span class="nav-disabled" aria-disabled="true" data-i18n="nav.infrastructure">Инфраструктура</span>
<span class="nav-disabled" aria-disabled="true" data-i18n="nav.settings">Настройки</span>
</nav>
</aside>
<main class="content" id="main-content">
<div class="page-head">
<div><h1 data-i18n="overview.title">Состояние Control Center</h1><p class="muted" data-i18n="overview.subtitle">Единая точка входа в состояние установки, сайта и узлов.</p></div>
<div class="actions"><span class="badge" aria-label="Риск неизвестен" data-i18n="risk.unknown"><span aria-hidden="true">?</span> Риск: неизвестен</span></div>
</div>
<section class="context-panel" aria-labelledby="context-title">
<h2 id="context-title" data-i18n="context.title">Контекст</h2>
<div class="context-grid" role="group" aria-label="Выбор контекста">
<button class="context" type="button" disabled aria-pressed="true"><strong data-i18n="context.installation">Установка</strong><span data-i18n="context.installation.current">Текущий контекст</span></button>
<button class="context" type="button" disabled aria-pressed="false"><strong data-i18n="context.site">Сайт</strong><span data-i18n="context.not_selected">Не выбран</span></button>
<button class="context" type="button" disabled aria-pressed="false"><strong data-i18n="context.node">Узел</strong><span data-i18n="context.not_selected">Не выбран</span></button>
</div>
</section>
<div class="cards">
<section class="card" aria-labelledby="health-title">
<h2 id="health-title" data-i18n="health.title">Состояние</h2>
<p class="status-line"><span class="status-icon" aria-hidden="true">◇</span><span><strong data-i18n="health.unavailable">Статус: данные не загружены</strong><small data-i18n="health.unavailable.help">Интерфейс не подменяет фактический health-check.</small></span></p>
<dl class="definition"><dt data-i18n="freshness.label">Актуальность</dt><dd data-i18n="freshness.unavailable">Нет данных</dd><dt data-i18n="environment.label">Среда</dt><dd data-i18n="environment.unavailable">Не определена</dd></dl>
</section>
<section class="card" aria-labelledby="notifications-title">
<h2 id="notifications-title" data-i18n="notifications.title">Уведомления</h2>
<p class="status-line"><span class="status-icon" aria-hidden="true">◇</span><span><strong data-i18n="notifications.disconnected">Источник уведомлений не подключён</strong><small data-i18n="notifications.disconnected.help">Количество активных событий не показывается без подтверждённого источника.</small></span></p>
</section>
<section class="card" aria-labelledby="session-title">
<h2 id="session-title" data-i18n="session.title">Сеанс</h2>
<dl class="definition"><dt data-i18n="session.user">Пользователь</dt><dd>{{.Identity.DisplayName}}</dd><dt data-i18n="session.login">Учётная запись</dt><dd>{{.Identity.Username}}</dd></dl>
</section>
<section class="card" aria-labelledby="safety-title">
<h2 id="safety-title" data-i18n="safety.title">Безопасное состояние</h2>
<p class="status-line"><span class="status-icon" aria-hidden="true">!</span><span><strong data-i18n="safety.fail_closed">Неподтверждённые данные отображаются как неизвестные</strong><small data-i18n="safety.fail_closed.help">Цвет не используется как единственный признак статуса или риска.</small></span></p>
</section>
</div>
<footer class="footer"><span class="muted" data-i18n="shell.locale">Язык интерфейса: русский (ru-RU)</span><form method="post" action="/web/logout"><button type="submit" data-i18n="session.logout">Выйти</button></form></footer>
</main>
</div>
</body>
</html>`))

func init() {
	overviewTemplate = productOverviewV2Template
}
