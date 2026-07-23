-- Optionaler generischer Webhook-Trigger je Agent: ein geheimes Token in der
-- URL (POST /api/trigger/{token}) legt eine Backlog-Aufgabe an und weckt den
-- Agenten — für Systeme ohne eigenes Zielsystem-Plugin (CI, Cron, Zapier, …).
-- NULL = Webhook deaktiviert (Default); Rotation ersetzt das Token.
ALTER TABLE agents ADD COLUMN webhook_token TEXT UNIQUE;
