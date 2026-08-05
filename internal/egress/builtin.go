package egress

// Built-in template catalogue: curated host sets for the usual egress needs of
// agents (package registries, code hosting, container images). The catalogue
// lives in the code — versioned, reviewable, identical for every org.
// Importing creates an ordinary org-owned template (a copy) that is freely
// editable afterwards; the catalogue itself stays unchanged.
//
// Deliberately NOT included: api.anthropic.com (it lives in the org's
// configurable base allowlist, seeded there) and org-specific systems such as
// one's own Zammad or GitLab — those are what custom templates are for.

// BuiltinHost is a host pattern of the catalogue together with its rationale.
type BuiltinHost struct {
	Pattern string `json:"pattern"`
	Note    string `json:"note"`
}

// BuiltinTemplate is a catalogue entry. Slug is the stable API identifier;
// Name becomes the template name on import (unique per org).
type BuiltinTemplate struct {
	Slug        string        `json:"slug"`
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Hosts       []BuiltinHost `json:"hosts"`
}

// Builtins is the catalogue. Patterns must pass NormalizePattern unchanged
// (lower case, no scheme/path/port) — guarded by a unit test.
var Builtins = []BuiltinTemplate{
	{
		Slug:        "github",
		Name:        "GitHub",
		Description: "Clone code, fetch releases and raw content, REST/GraphQL API",
		Hosts: []BuiltinHost{
			{Pattern: "github.com", Note: "Git over HTTPS, web URLs"},
			{Pattern: "api.github.com", Note: "REST and GraphQL API"},
			{Pattern: "codeload.github.com", Note: "archive downloads (zip/tar) — the github plugin's checkout"},
			{Pattern: "*.githubusercontent.com", Note: "raw content, release assets, avatars, issue attachments"},
			// GitHub Actions hands job logs out as a redirect to Azure blob
			// storage. Without this host get_job_log fails at the redirect, and
			// the agent cannot diagnose a red CI run.
			{Pattern: "*.blob.core.windows.net", Note: "GitHub Actions job logs (redirect target)"},
		},
	},
	{
		Slug:        "gitlab-com",
		Name:        "GitLab.com",
		Description: "Repos and API on gitlab.com (not for self-hosted instances)",
		Hosts: []BuiltinHost{
			{Pattern: "gitlab.com", Note: "Git over HTTPS, API"},
			{Pattern: "registry.gitlab.com", Note: "container registry"},
		},
	},
	{
		Slug:        "npm",
		Name:        "Node.js / npm",
		Description: "Install packages via npm, yarn or pnpm",
		Hosts: []BuiltinHost{
			{Pattern: "registry.npmjs.org", Note: "npm registry"},
			{Pattern: "registry.yarnpkg.com", Note: "Yarn mirror of the npm registry"},
		},
	},
	{
		Slug:        "pypi",
		Name:        "Python / PyPI",
		Description: "Install packages via pip or uv",
		Hosts: []BuiltinHost{
			{Pattern: "pypi.org", Note: "package index"},
			{Pattern: "files.pythonhosted.org", Note: "package downloads"},
		},
	},
	{
		Slug:        "go",
		Name:        "Go modules",
		Description: "Fetch modules through the public Go module proxy",
		Hosts: []BuiltinHost{
			{Pattern: "proxy.golang.org", Note: "module proxy"},
			{Pattern: "sum.golang.org", Note: "checksum database"},
			{Pattern: "index.golang.org", Note: "module index"},
		},
	},
	{
		Slug:        "rust",
		Name:        "Rust / crates.io",
		Description: "Install crates via cargo",
		Hosts: []BuiltinHost{
			{Pattern: "crates.io", Note: "registry API"},
			{Pattern: "index.crates.io", Note: "sparse index"},
			{Pattern: "static.crates.io", Note: "crate downloads"},
		},
	},
	{
		Slug:        "composer",
		Name:        "PHP / Composer",
		Description: "Install packages via composer",
		Hosts: []BuiltinHost{
			{Pattern: "packagist.org", Note: "package index"},
			{Pattern: "repo.packagist.org", Note: "metadata (p2/…); the dist archives come via GitHub"},
		},
	},
	{
		Slug:        "dart-flutter",
		Name:        "Dart / Flutter",
		Description: "Fetch Flutter SDKs via fvm and install packages via pub",
		Hosts: []BuiltinHost{
			{Pattern: "pub.dev", Note: "package registry including the archives (pub get)"},
			// storage.googleapis.com is how SDK and engine artefacts are obtained
			// (flutter_infra_release, download.flutter.io). The proxy does not
			// terminate TLS and only matches the CONNECT host — a path-precise
			// version is therefore impossible, and this entry inevitably opens up
			// EVERY public GCS bucket. Whoever does not want that mirrors the
			// artefacts themselves and sets FLUTTER_STORAGE_BASE_URL in the sandbox.
			{Pattern: "storage.googleapis.com", Note: "Flutter SDK and engine artefacts — CAUTION: covers all public GCS buckets"},
		},
	},
	{
		Slug:        "maven-gradle",
		Name:        "Maven / Gradle",
		Description: "Fetch dependencies and Gradle distributions for JVM projects",
		Hosts: []BuiltinHost{
			{Pattern: "repo1.maven.org", Note: "Maven Central"},
			{Pattern: "plugins.gradle.org", Note: "Gradle Plugin Portal (metadata)"},
			// The Plugin Portal does not serve the artefacts itself, it redirects to
			// plugins-artifacts.gradle.org. Without this host every `plugins { id … }`
			// fails — the proxy follows no redirect, it only ever sees the next
			// CONNECT host.
			{Pattern: "plugins-artifacts.gradle.org", Note: "artefact downloads of the Plugin Portal (redirect target)"},
			{Pattern: "services.gradle.org", Note: "wrapper distributions; redirects to GitHub Releases — GitHub template required"},
			{Pattern: "api.foojay.io", Note: "JDK provisioning for Gradle toolchains; redirects to GitHub Releases"},
		},
	},
	{
		Slug:        "android",
		Name:        "Android / Google Maven",
		Description: "Fetch Android dependencies for Gradle and Flutter builds",
		Hosts: []BuiltinHost{
			{Pattern: "maven.google.com", Note: "Google Maven index (redirects to dl.google.com)"},
			{Pattern: "dl.google.com", Note: "artefact downloads of the Google Maven repo"},
		},
	},
	{
		Slug:        "container",
		Name:        "Container registries",
		Description: "Pull images from Docker Hub, GitHub and Quay",
		Hosts: []BuiltinHost{
			{Pattern: "registry-1.docker.io", Note: "Docker Hub Registry"},
			{Pattern: "auth.docker.io", Note: "Docker Hub auth"},
			{Pattern: "production.cloudflare.docker.com", Note: "Docker Hub layer CDN"},
			{Pattern: "ghcr.io", Note: "GitHub Container Registry"},
			{Pattern: "quay.io", Note: "Red Hat Quay"},
		},
	},
	{
		Slug:        "debian-ubuntu",
		Name:        "Debian/Ubuntu packages",
		Description: "Install system packages via apt (extend the sandbox image)",
		Hosts: []BuiltinHost{
			{Pattern: "deb.debian.org", Note: "Debian mirror (CDN)"},
			{Pattern: "security.debian.org", Note: "Debian security updates"},
			{Pattern: "archive.ubuntu.com", Note: "Ubuntu main archive"},
			{Pattern: "security.ubuntu.com", Note: "Ubuntu security updates"},
		},
	},
}

// BuiltinBySlug returns the catalogue entry for a slug.
func BuiltinBySlug(slug string) (BuiltinTemplate, bool) {
	for _, b := range Builtins {
		if b.Slug == slug {
			return b, true
		}
	}
	return BuiltinTemplate{}, false
}
