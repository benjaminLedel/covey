-- Welchen Runner ein Agent braucht (spec/16, "Scheduling").
--
-- Tags sind die Faehigkeitsaussage des Hosts: arm64, gpu, ein Runner im Netz
-- des Zielsystems. Ein Agent, der eine davon braucht, nennt sie — und bekommt
-- nur Runner, die alle nennen. Die andere Richtung gilt nicht: ein Runner darf
-- mehr tragen, als von ihm verlangt wird.
--
-- Leer ist der Normalfall und heisst "jeder Runner der Organisation".
ALTER TABLE agents ADD COLUMN runner_tags TEXT[] NOT NULL DEFAULT '{}';
