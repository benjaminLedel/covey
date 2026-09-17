/// <reference types="vite/client" />

// Vite's environment types: they declare the non-JS imports the bundler
// resolves (`import "./styles.css"`, assets) and import.meta.env. TypeScript 5
// let the side-effect import pass without a declaration, TypeScript 7 insists
// on one (TS2882) — rightly, it was simply unchecked before.
