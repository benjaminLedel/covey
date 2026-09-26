package push

// The words of a notification, in the language the device registered with.
// A notification is written on the instance, not in the app — the app is
// not running when it arrives — so the instance carries these few lines for
// the app's ten languages.
//
// Per kind two forms: the sentence a notification without preview says
// after the agent's name ("has a question"), and the short label beside the
// name when the first line of the content follows.

type words struct{ sentence, label string }

var texts = map[string]map[string]words{
	"de": {
		"question": {"hat eine Rückfrage", "Rückfrage"},
		"answer":   {"hat geantwortet", "Antwort"},
		"result":   {"hat eine Aufgabe erledigt", "Erledigt"},
		"error":    {"kommt bei einer Aufgabe nicht weiter", "Fehlgeschlagen"},
	},
	"en": {
		"question": {"has a question", "Question"},
		"answer":   {"replied", "Reply"},
		"result":   {"finished a task", "Done"},
		"error":    {"got stuck on a task", "Failed"},
	},
	"es": {
		"question": {"tiene una pregunta", "Pregunta"},
		"answer":   {"ha respondido", "Respuesta"},
		"result":   {"ha terminado una tarea", "Hecho"},
		"error":    {"se ha atascado en una tarea", "Fallido"},
	},
	"fr": {
		"question": {"a une question", "Question"},
		"answer":   {"a répondu", "Réponse"},
		"result":   {"a terminé une tâche", "Terminé"},
		"error":    {"est bloqué sur une tâche", "Échec"},
	},
	"it": {
		"question": {"ha una domanda", "Domanda"},
		"answer":   {"ha risposto", "Risposta"},
		"result":   {"ha completato un compito", "Fatto"},
		"error":    {"si è bloccato su un compito", "Non riuscito"},
	},
	"nl": {
		"question": {"heeft een vraag", "Vraag"},
		"answer":   {"heeft geantwoord", "Antwoord"},
		"result":   {"heeft een taak afgerond", "Klaar"},
		"error":    {"loopt vast op een taak", "Mislukt"},
	},
	"pl": {
		"question": {"ma pytanie", "Pytanie"},
		"answer":   {"odpowiedział(a)", "Odpowiedź"},
		"result":   {"ukończył(a) zadanie", "Gotowe"},
		"error":    {"utknął/utknęła przy zadaniu", "Niepowodzenie"},
	},
	"pt": {
		"question": {"tem uma pergunta", "Pergunta"},
		"answer":   {"respondeu", "Resposta"},
		"result":   {"concluiu uma tarefa", "Concluído"},
		"error":    {"ficou bloqueado numa tarefa", "Falhou"},
	},
	"ja": {
		"question": {"から確認があります", "確認"},
		"answer":   {"が返信しました", "返信"},
		"result":   {"がタスクを完了しました", "完了"},
		"error":    {"がタスクで行き詰まりました", "失敗"},
	},
	"zh": {
		"question": {"有一个问题", "问题"},
		"answer":   {"已回复", "回复"},
		"result":   {"完成了一项任务", "已完成"},
		"error":    {"在一项任务上卡住了", "失败"},
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
