package push

// The words of a notification, in the language the device registered with.
// A notification is written on the instance, not in the app — the app is
// not running when it arrives — so the instance carries these few lines for
// the app's ten languages.
//
// Per kind two forms: the sentence a notification without preview says
// after the agent's name ("has a question"), and the short label beside the
// name when the first line of the content follows.
//
// A decision that waits (#622) has its own: an approval, a configuration
// proposal from the chat, an open point of a review — after the agent's
// name — and a workflow's approval step, after the workflow's.

type words struct{ sentence, label string }

var texts = map[string]map[string]words{
	"de": {
		"question": {"hat eine Rückfrage", "Rückfrage"},
		"answer":   {"hat geantwortet", "Antwort"},
		"result":   {"hat eine Aufgabe erledigt", "Erledigt"},
		"error":    {"kommt bei einer Aufgabe nicht weiter", "Fehlgeschlagen"},
		"approval": {"wartet auf eine Freigabe", "Freigabe"},
		"proposal": {"schlägt eine Konfigurationsänderung vor", "Konfigurationsvorschlag"},
		"review":   {"hat einen offenen Punkt aus einem Review", "Offener Punkt"},
		"decision": {"wartet auf eine Entscheidung", "Entscheidung"},
	},
	"en": {
		"question": {"has a question", "Question"},
		"answer":   {"replied", "Reply"},
		"result":   {"finished a task", "Done"},
		"error":    {"got stuck on a task", "Failed"},
		"approval": {"waits for an approval", "Approval"},
		"proposal": {"proposes a configuration change", "Configuration proposal"},
		"review":   {"has an open point from a review", "Open point"},
		"decision": {"waits for a decision", "Decision"},
	},
	"es": {
		"question": {"tiene una pregunta", "Pregunta"},
		"answer":   {"ha respondido", "Respuesta"},
		"result":   {"ha terminado una tarea", "Hecho"},
		"error":    {"se ha atascado en una tarea", "Fallido"},
		"approval": {"espera una aprobación", "Aprobación"},
		"proposal": {"propone un cambio de configuración", "Propuesta de configuración"},
		"review":   {"tiene un punto abierto de una revisión", "Punto abierto"},
		"decision": {"espera una decisión", "Decisión"},
	},
	"fr": {
		"question": {"a une question", "Question"},
		"answer":   {"a répondu", "Réponse"},
		"result":   {"a terminé une tâche", "Terminé"},
		"error":    {"est bloqué sur une tâche", "Échec"},
		"approval": {"attend une validation", "Validation"},
		"proposal": {"propose une modification de configuration", "Proposition de configuration"},
		"review":   {"a un point ouvert issu d'une revue", "Point ouvert"},
		"decision": {"attend une décision", "Décision"},
	},
	"it": {
		"question": {"ha una domanda", "Domanda"},
		"answer":   {"ha risposto", "Risposta"},
		"result":   {"ha completato un compito", "Fatto"},
		"error":    {"si è bloccato su un compito", "Non riuscito"},
		"approval": {"attende un'approvazione", "Approvazione"},
		"proposal": {"propone una modifica alla configurazione", "Proposta di configurazione"},
		"review":   {"ha un punto aperto da una revisione", "Punto aperto"},
		"decision": {"attende una decisione", "Decisione"},
	},
	"nl": {
		"question": {"heeft een vraag", "Vraag"},
		"answer":   {"heeft geantwoord", "Antwoord"},
		"result":   {"heeft een taak afgerond", "Klaar"},
		"error":    {"loopt vast op een taak", "Mislukt"},
		"approval": {"wacht op goedkeuring", "Goedkeuring"},
		"proposal": {"stelt een configuratiewijziging voor", "Configuratievoorstel"},
		"review":   {"heeft een open punt uit een review", "Open punt"},
		"decision": {"wacht op een beslissing", "Beslissing"},
	},
	"pl": {
		"question": {"ma pytanie", "Pytanie"},
		"answer":   {"odpowiedział(a)", "Odpowiedź"},
		"result":   {"ukończył(a) zadanie", "Gotowe"},
		"error":    {"utknął/utknęła przy zadaniu", "Niepowodzenie"},
		"approval": {"czeka na zatwierdzenie", "Zatwierdzenie"},
		"proposal": {"proponuje zmianę konfiguracji", "Propozycja konfiguracji"},
		"review":   {"ma otwarty punkt z przeglądu", "Otwarty punkt"},
		"decision": {"czeka na decyzję", "Decyzja"},
	},
	"pt": {
		"question": {"tem uma pergunta", "Pergunta"},
		"answer":   {"respondeu", "Resposta"},
		"result":   {"concluiu uma tarefa", "Concluído"},
		"error":    {"ficou bloqueado numa tarefa", "Falhou"},
		"approval": {"aguarda uma aprovação", "Aprovação"},
		"proposal": {"propõe uma alteração de configuração", "Proposta de configuração"},
		"review":   {"tem um ponto em aberto de uma revisão", "Ponto em aberto"},
		"decision": {"aguarda uma decisão", "Decisão"},
	},
	"ja": {
		"question": {"から確認があります", "確認"},
		"answer":   {"が返信しました", "返信"},
		"result":   {"がタスクを完了しました", "完了"},
		"error":    {"がタスクで行き詰まりました", "失敗"},
		"approval": {"が承認を待っています", "承認"},
		"proposal": {"が設定変更を提案しています", "設定の提案"},
		"review":   {"にレビューの未解決事項があります", "未解決事項"},
		"decision": {"が判断を待っています", "判断"},
	},
	"zh": {
		"question": {"有一个问题", "问题"},
		"answer":   {"已回复", "回复"},
		"result":   {"完成了一项任务", "已完成"},
		"error":    {"在一项任务上卡住了", "失败"},
		"approval": {"正在等待批准", "批准"},
		"proposal": {"提议修改配置", "配置提议"},
		"review":   {"有一项来自审查的待处理事项", "待处理事项"},
		"decision": {"正在等待决定", "决定"},
	},
}

// Compose writes the title and body of a notification about kind from the
// agent named name. With preview the title names agent and kind and the
// body is the first line of what was said; without, one line says what
// happened and there is no body.
func Compose(lang, kind, name, line string, preview bool) (title, body string) {
	t, ok := texts[lang]
	if !ok {
		t = texts["en"]
	}
	w, ok := t[kind]
	if !ok {
		w = t["answer"]
	}
	if preview && line != "" {
		return Clip(name+" · "+w.label, MaxTitle), Clip(line, MaxBody)
	}
	// Japanese and Chinese join the name without a space.
	sep := " "
	if lang == "ja" || lang == "zh" {
		sep = ""
	}
	return Clip(name+sep+w.sentence, MaxTitle), ""
}
