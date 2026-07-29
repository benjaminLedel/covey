/// <reference types="vite/client" />

// Vites Umgebungstypen: sie deklarieren die Nicht-JS-Importe, die der Bundler
// auflöst (`import "./styles.css"`, Assets) und import.meta.env. TypeScript 5
// ließ den Seiteneffekt-Import ohne Deklaration durchgehen, TypeScript 7
// besteht darauf (TS2882) — zu Recht, es war vorher schlicht ungeprüft.
