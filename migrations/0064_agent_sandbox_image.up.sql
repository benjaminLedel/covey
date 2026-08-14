-- Das Sandbox-Image gehoert dem Agenten, nicht der Instanz (D11 in spec/07,
-- ausgefuehrt in spec/16-runner.md).
--
-- Bisher entschied COVEY_SANDBOX_IMAGE fuer alle: der Mail-Agent trug die JVM
-- des Entwickler-Agenten mit, und ein Runner konnte nicht sagen, fuer wen er
-- ueberhaupt in Frage kommt — das Image ist auf ihm eine Kapazitaetsaussage.
--
-- Der Wert ist entweder ein Profilname (base, dev) oder eine Image-Referenz.
-- Beides in einer Spalte, weil es dieselbe Frage beantwortet ("worin laeuft
-- dieser Agent") und die Aufloesung genau eine Regel kennt: ist es ein
-- bekanntes Profil, gilt dessen Image, sonst der Wert selbst. Leer = das
-- Instanz-Default, damit eine Konfiguration ohne Angabe bleibt, was sie war.
ALTER TABLE agents ADD COLUMN sandbox_image TEXT NOT NULL DEFAULT '';

-- Bestandsagenten bekommen 'dev' und damit genau das Image, in dem sie bisher
-- liefen — das alte covey-sandbox:latest trug PHP, JDK, fvm und uv. Ohne diese
-- Zeile wuerde ein Upgrade jedem laufenden Entwickler-Agenten seine Toolchain
-- unter den Fuessen wegziehen. Wer seinen Support-Agenten schlanker haben will,
-- stellt ihn in der Oberflaeche auf 'base' — eine Entscheidung, die ein Mensch
-- trifft, nicht eine, die eine Migration fuer ihn faellt.
UPDATE agents SET sandbox_image = 'dev';
