package egress

// Built-in-Template-Katalog: kuratierte Host-Sets für die üblichen Egress-
// Bedürfnisse von Agenten (Paket-Registries, Code-Hosting, Container-Images).
// Der Katalog lebt im Code — versioniert, reviewbar, für alle Orgs gleich.
// Übernehmen erzeugt ein normales org-eigenes Template (Kopie), das danach
// frei editierbar ist; der Katalog selbst bleibt unverändert.
//
// Bewusst NICHT enthalten: api.anthropic.com (liegt in der konfigurierbaren
// Basis-Allowlist der Org, dort geseedet) und org-spezifische Systeme wie das
// eigene Zammad oder GitLab — dafür sind eigene Templates da.

// BuiltinHost ist ein Host-Muster des Katalogs samt Begründung.
type BuiltinHost struct {
	Pattern string `json:"pattern"`
	Note    string `json:"note"`
}

// BuiltinTemplate ist ein Katalog-Eintrag. Slug ist der stabile API-Bezeichner,
// Name wird beim Übernehmen zum Template-Namen (unique je Org).
type BuiltinTemplate struct {
	Slug        string        `json:"slug"`
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Hosts       []BuiltinHost `json:"hosts"`
}

// Builtins ist der Katalog. Muster müssen NormalizePattern unverändert
// passieren (klein, ohne Schema/Pfad/Port) — abgesichert per Unit-Test.
var Builtins = []BuiltinTemplate{
	{
		Slug:        "github",
		Name:        "GitHub",
		Description: "Code klonen, Releases und Raw-Inhalte laden, REST-/GraphQL-API",
		Hosts: []BuiltinHost{
			{Pattern: "github.com", Note: "Git über HTTPS, Web-URLs"},
			{Pattern: "api.github.com", Note: "REST- und GraphQL-API"},
			{Pattern: "codeload.github.com", Note: "Archiv-Downloads (zip/tar)"},
			{Pattern: "*.githubusercontent.com", Note: "Raw-Inhalte, Release-Assets, Avatare"},
		},
	},
	{
		Slug:        "gitlab-com",
		Name:        "GitLab.com",
		Description: "Repos und API auf gitlab.com (nicht für selbst gehostete Instanzen)",
		Hosts: []BuiltinHost{
			{Pattern: "gitlab.com", Note: "Git über HTTPS, API"},
			{Pattern: "registry.gitlab.com", Note: "Container-Registry"},
		},
	},
	{
		Slug:        "npm",
		Name:        "Node.js / npm",
		Description: "Pakete via npm, yarn oder pnpm installieren",
		Hosts: []BuiltinHost{
			{Pattern: "registry.npmjs.org", Note: "npm-Registry"},
			{Pattern: "registry.yarnpkg.com", Note: "Yarn-Spiegel der npm-Registry"},
		},
	},
	{
		Slug:        "pypi",
		Name:        "Python / PyPI",
		Description: "Pakete via pip oder uv installieren",
		Hosts: []BuiltinHost{
			{Pattern: "pypi.org", Note: "Paket-Index"},
			{Pattern: "files.pythonhosted.org", Note: "Paket-Downloads"},
		},
	},
	{
		Slug:        "go",
		Name:        "Go-Module",
		Description: "Module über den öffentlichen Go-Modul-Proxy laden",
		Hosts: []BuiltinHost{
			{Pattern: "proxy.golang.org", Note: "Modul-Proxy"},
			{Pattern: "sum.golang.org", Note: "Checksummen-Datenbank"},
			{Pattern: "index.golang.org", Note: "Modul-Index"},
		},
	},
	{
		Slug:        "rust",
		Name:        "Rust / crates.io",
		Description: "Crates via cargo installieren",
		Hosts: []BuiltinHost{
			{Pattern: "crates.io", Note: "Registry-API"},
			{Pattern: "index.crates.io", Note: "Sparse-Index"},
			{Pattern: "static.crates.io", Note: "Crate-Downloads"},
		},
	},
	{
		Slug:        "composer",
		Name:        "PHP / Composer",
		Description: "Pakete via composer installieren",
		Hosts: []BuiltinHost{
			{Pattern: "packagist.org", Note: "Paket-Index"},
			{Pattern: "repo.packagist.org", Note: "Metadaten (p2/…); die dist-Archive kommen über GitHub"},
		},
	},
	{
		Slug:        "dart-flutter",
		Name:        "Dart / Flutter",
		Description: "Flutter-SDKs via fvm holen und Pakete via pub installieren",
		Hosts: []BuiltinHost{
			{Pattern: "pub.dev", Note: "Paket-Registry inkl. der Archive (pub get)"},
			// storage.googleapis.com ist der Bezugsweg für SDK- und Engine-Artefakte
			// (flutter_infra_release, download.flutter.io). Der Proxy terminiert TLS
			// nicht und matcht nur den CONNECT-Host — eine pfadgenaue Fassung ist
			// deshalb nicht möglich, und der Eintrag öffnet zwangsläufig JEDEN
			// öffentlichen GCS-Bucket. Wer das nicht will, spiegelt die Artefakte
			// selbst und setzt FLUTTER_STORAGE_BASE_URL in der Sandbox.
			{Pattern: "storage.googleapis.com", Note: "Flutter-SDK- und Engine-Artefakte — ACHTUNG: deckt alle öffentlichen GCS-Buckets ab"},
		},
	},
	{
		Slug:        "maven-gradle",
		Name:        "Maven / Gradle",
		Description: "Abhängigkeiten und Gradle-Distributionen für JVM-Projekte laden",
		Hosts: []BuiltinHost{
			{Pattern: "repo1.maven.org", Note: "Maven Central"},
			{Pattern: "plugins.gradle.org", Note: "Gradle Plugin Portal (Metadaten)"},
			// Das Plugin Portal liefert die Artefakte selbst nicht aus, sondern
			// leitet auf plugins-artifacts.gradle.org um. Ohne diesen Host scheitert
			// jedes `plugins { id … }` — der Proxy folgt keinem Redirect, er sieht
			// nur den nächsten CONNECT-Host.
			{Pattern: "plugins-artifacts.gradle.org", Note: "Artefakt-Downloads des Plugin Portals (Redirect-Ziel)"},
			{Pattern: "services.gradle.org", Note: "Wrapper-Distributionen; leitet auf GitHub Releases um — GitHub-Template nötig"},
			{Pattern: "api.foojay.io", Note: "JDK-Beschaffung für Gradle-Toolchains; leitet auf GitHub Releases um"},
		},
	},
	{
		Slug:        "android",
		Name:        "Android / Google Maven",
		Description: "Android-Abhängigkeiten für Gradle- und Flutter-Builds laden",
		Hosts: []BuiltinHost{
			{Pattern: "maven.google.com", Note: "Google-Maven-Index (leitet auf dl.google.com um)"},
			{Pattern: "dl.google.com", Note: "Artefakt-Downloads des Google-Maven-Repos"},
		},
	},
	{
		Slug:        "container",
		Name:        "Container-Registries",
		Description: "Images von Docker Hub, GitHub und Quay ziehen",
		Hosts: []BuiltinHost{
			{Pattern: "registry-1.docker.io", Note: "Docker Hub Registry"},
			{Pattern: "auth.docker.io", Note: "Docker Hub Auth"},
			{Pattern: "production.cloudflare.docker.com", Note: "Docker Hub Layer-CDN"},
			{Pattern: "ghcr.io", Note: "GitHub Container Registry"},
			{Pattern: "quay.io", Note: "Red Hat Quay"},
		},
	},
	{
		Slug:        "debian-ubuntu",
		Name:        "Debian/Ubuntu-Pakete",
		Description: "Systempakete via apt installieren (Sandbox-Image erweitern)",
		Hosts: []BuiltinHost{
			{Pattern: "deb.debian.org", Note: "Debian-Spiegel (CDN)"},
			{Pattern: "security.debian.org", Note: "Debian-Security-Updates"},
			{Pattern: "archive.ubuntu.com", Note: "Ubuntu-Hauptarchiv"},
			{Pattern: "security.ubuntu.com", Note: "Ubuntu-Security-Updates"},
		},
	},
}

// BuiltinBySlug liefert den Katalog-Eintrag zum Slug.
func BuiltinBySlug(slug string) (BuiltinTemplate, bool) {
	for _, b := range Builtins {
		if b.Slug == slug {
			return b, true
		}
	}
	return BuiltinTemplate{}, false
}
