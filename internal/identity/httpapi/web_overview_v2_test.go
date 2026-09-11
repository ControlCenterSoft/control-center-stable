package httpapi

import (
	"bytes"
	"strings"
	"testing"

	"control-center/internal/identity/auth"
)

func TestProductOverviewV2ExposesRequiredShellWithoutInventingState(t *testing.T) {
	var rendered bytes.Buffer
	data := struct {
		Version  string
		Identity auth.Identity
	}{
		Version: "0.29.0-test",
		Identity: auth.Identity{
			Username:    `<script>alert("x")</script>`,
			DisplayName: "Тестовый администратор",
		},
	}

	if err := productOverviewV2Template.Execute(&rendered, data); err != nil {
		t.Fatalf("render overview v2: %v", err)
	}
	body := rendered.String()

	required := []string{
		`lang="ru"`,
		`data-locale="ru-RU"`,
		`name="viewport"`,
		"0.29.0-test",
		"Установка",
		"Сайт",
		"Узел",
		"Среда: не определена",
		"Актуальность",
		"Нет данных",
		"Риск: неизвестен",
		"Статус: данные не загружены",
		"Источник уведомлений не подключён",
		"Цвет не используется как единственный признак статуса или риска",
		"Тестовый администратор",
		`data-i18n="overview.title"`,
		`aria-label="Выбор контекста"`,
	}
	for _, want := range required {
		if !strings.Contains(body, want) {
			t.Errorf("overview v2 is missing %q", want)
		}
	}

	if strings.Contains(body, `<script>alert("x")</script>`) {
		t.Fatal("overview v2 rendered an unescaped username")
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Fatal("overview v2 did not render the escaped username")
	}
}

func TestProductOverviewV2IsActiveOverviewTemplate(t *testing.T) {
	if overviewTemplate != productOverviewV2Template {
		t.Fatal("product overview v2 template is not active")
	}
}
