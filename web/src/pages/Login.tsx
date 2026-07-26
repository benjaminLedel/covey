/* Die öffentliche Website ist auf mehrere Seiten gewachsen (Home, Funktion,
   Produktseiten) und lebt jetzt unter web/src/public/. Diese Datei bleibt als
   Re-Export bestehen, damit ältere Importpfade nicht brechen. */
export { default } from "../public/PublicSite";
