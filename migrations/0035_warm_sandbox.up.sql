-- Warme Sandbox (opt-in pro Agent): hält die Sandbox zwischen Wach-Phasen live,
-- statt sie beim Einschlafen abzubauen. Der Dev-Server und Caches (node_modules,
-- Build) überleben — für Agenten wie den QA-Tester, die sonst jeden Lauf von null
-- aufsetzen. Default false: alle anderen Agenten bleiben ephemer ("dumm und
-- ersetzbar", spec/01).
ALTER TABLE agents ADD COLUMN warm_sandbox boolean NOT NULL DEFAULT false;
