-- Bisher konnte ein Agent nur an einen Menschen berichten: agents.supervisor_id
-- trug einen Fremdschlüssel auf humans(id) (Migration 0006). Damit sich Agenten
-- auch anderen Agenten unterordnen lassen, wird diese FK-Bindung gelöst.
-- supervisor_id verweist nun polymorph auf einen Menschen ODER einen Agenten
-- derselben Organisation. Die referenzielle Integrität beim Löschen wird
-- stattdessen in der Anwendung gepflegt (agents.Delete und org.DeleteHuman
-- setzen verweisende supervisor_id auf NULL).
ALTER TABLE agents DROP CONSTRAINT IF EXISTS agents_supervisor_id_fkey;
