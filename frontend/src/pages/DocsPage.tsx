import { useState, useRef } from 'react'
import { BookOpen, Check, Copy } from 'lucide-react'
import styles from './DocsPage.module.css'

interface CodeExample { label?: string; lang: string; content: string }
interface FormatSection { title: string; text?: string; note?: string; codes: CodeExample[] }
interface Format {
  id: string
  name: string
  icon: string
  iconUrl?: string
  description: string
  sections: (base: string) => FormatSection[]
}
interface StepProps {
  num: number
  title: string
  text: string
  screenshot?: { src: string; alt: string; caption?: string }
  code?: { lang: string; content: string }
  note?: string
}

function CodeBlock({ lang, content }: { lang: string; content: string }) {
  const [copied, setCopied] = useState(false)
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const copy = () => {
    void navigator.clipboard.writeText(content).then(() => {
      setCopied(true)
      if (timerRef.current) clearTimeout(timerRef.current)
      timerRef.current = setTimeout(() => setCopied(false), 2000)
    })
  }
  return (
    <div className={styles.codeBlock}>
      <div className={styles.codeHeader}>
        <span className={styles.codeLang}>{lang}</span>
        <button className={`${styles.copyBtn} ${copied ? styles.copied : ''}`} onClick={copy}>
          {copied ? <Check size={11} /> : <Copy size={11} />}
          {copied ? 'Copied!' : 'Copy'}
        </button>
      </div>
      <div className={styles.codeBody}>
        <pre>{content}</pre>
      </div>
    </div>
  )
}

function UrlBlock({ url }: { url: string }) {
  const [copied, setCopied] = useState(false)
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  return (
    <div className={styles.urlBlock}>
      <span className={styles.urlValue}>{url}</span>
      <button
        className={styles.urlCopyBtn}
        onClick={() => { void navigator.clipboard.writeText(url).then(() => { setCopied(true); if (timerRef.current) clearTimeout(timerRef.current); timerRef.current = setTimeout(() => setCopied(false), 2000) }) }}
      >
        {copied ? <Check size={11} /> : <Copy size={11} />}
        {copied ? 'Copied' : 'Copy'}
      </button>
    </div>
  )
}

function SectionBlock({ section }: { section: FormatSection }) {
  return (
    <div>
      <p className={styles.blockTitle}>{section.title}</p>
      {section.text && <p className={styles.blockText}>{section.text}</p>}
      {section.note && <div className={styles.noteBox}>⚠ {section.note}</div>}
      {section.codes.map((c, i) => (
        <div key={i}>
          {c.label && <p className={styles.codeLabel}>{c.label}</p>}
          <CodeBlock lang={c.lang} content={c.content} />
        </div>
      ))}
    </div>
  )
}

function ScreenshotPlaceholder({ alt, src }: { alt: string; src: string }) {
  const filename = src.split('/').pop() ?? src
  return (
    <div className={styles.screenshotPlaceholder}>
      <span className={styles.screenshotPlaceholderLabel}>📸 Screenshot</span>
      <span className={styles.screenshotPlaceholderName}>{alt}</span>
      <span className={styles.screenshotPlaceholderPath}>
        frontend/public/docs/screenshots/{filename}
      </span>
    </div>
  )
}

function Screenshot({ src, alt, caption }: { src: string; alt: string; caption?: string }) {
  const [failed, setFailed] = useState(false)
  if (failed) return <ScreenshotPlaceholder alt={alt} src={src} />
  return (
    <div className={styles.screenshotWrap}>
      <img
        src={src}
        alt={alt}
        className={styles.screenshotImg}
        onError={() => setFailed(true)}
      />
      {caption && <p className={styles.screenshotCaption}>{caption}</p>}
    </div>
  )
}

export function Step({ num, title, text, screenshot, code, note }: StepProps) {
  return (
    <div className={styles.step}>
      <div className={styles.stepNum}>{num}</div>
      <div className={styles.stepBody}>
        <p className={styles.stepTitle}>{title}</p>
        <p className={styles.stepText}>{text}</p>
        {note && <div className={styles.noteBox}>⚠ {note}</div>}
        {code && <CodeBlock lang={code.lang} content={code.content} />}
        {screenshot && <Screenshot {...screenshot} />}
      </div>
    </div>
  )
}

const FORMATS: Format[] = [
  {
    id: 'maven',
    name: 'Maven 2/3',
    icon: '☕',
    description: 'Host, proxy, and group Maven repositories for Java artifacts (JAR, WAR, POM files). Fully compatible with Maven 2 and Maven 3.',
    sections: (base) => [
      {
        title: 'Repository URL',
        text: 'Use these endpoints in settings.xml or pom.xml:',
        codes: [{ lang: 'text', content: `${base}/repository/maven-releases/\n${base}/repository/maven-snapshots/\n${base}/repository/maven-central/   ← proxy cache` }],
      },
      {
        title: 'Configure ~/.m2/settings.xml',
        text: 'Route all Maven traffic through Nexspence and set credentials:',
        codes: [{ lang: 'xml', content: `<settings>
  <servers>
    <server>
      <id>nexspence</id>
      <username>admin</username>
      <password>admin123</password>
    </server>
  </servers>
  <mirrors>
    <mirror>
      <id>nexspence</id>
      <url>${base}/repository/maven-public/</url>
      <mirrorOf>*</mirrorOf>
    </mirror>
  </mirrors>
</settings>` }],
      },
      {
        title: 'Publish an Artifact',
        codes: [
          { label: 'Using mvn deploy plugin:', lang: 'bash', content: `mvn deploy:deploy-file \\
  -DrepositoryId=nexspence \\
  -Durl=${base}/repository/maven-releases/ \\
  -Dfile=myapp-1.0.jar \\
  -DgroupId=com.example \\
  -DartifactId=myapp \\
  -Dversion=1.0` },
          { label: 'Using curl (direct PUT):', lang: 'bash', content: `curl -u admin:admin123 \\
  -T myapp-1.0.jar \\
  "${base}/repository/maven-releases/com/example/myapp/1.0/myapp-1.0.jar"` },
        ],
      },
      {
        title: 'Download an Artifact',
        codes: [{ lang: 'bash', content: `curl -u admin:admin123 \\
  -O "${base}/repository/maven-releases/com/example/myapp/1.0/myapp-1.0.jar"` }],
      },
    ],
  },
  {
    id: 'npm',
    name: 'npm',
    icon: '📦',
    description: 'Host and proxy npm packages. Supports npm publish, install, and the full npm registry protocol.',
    sections: (base) => [
      {
        title: 'Repository URLs',
        codes: [{ lang: 'text', content: `${base}/repository/npm-hosted/   ← publish target\n${base}/repository/npm-proxy/    ← proxy to npmjs.com\n${base}/repository/npm-group/    ← combined group` }],
      },
      {
        title: 'Configure .npmrc',
        text: 'Add to your project .npmrc or ~/.npmrc:',
        codes: [{ lang: 'ini', content: `registry=${base}/repository/npm-group/
//${base.replace(/^https?:\/\//, '')}/repository/npm-hosted/:_authToken=nxs_your_token_here` }],
      },
      {
        title: 'Publish a Package',
        codes: [
          { label: 'Using npm publish:', lang: 'bash', content: `npm publish --registry ${base}/repository/npm-hosted/` },
          { label: 'Using curl (upload tarball):', lang: 'bash', content: `npm pack
curl -u admin:admin123 \\
  -H "Content-Type: application/octet-stream" \\
  -T mypackage-1.0.0.tgz \\
  "${base}/repository/npm-hosted/mypackage/-/mypackage-1.0.0.tgz"` },
        ],
      },
      {
        title: 'Install a Package',
        codes: [
          { label: 'Using npm:', lang: 'bash', content: `npm install mypackage --registry ${base}/repository/npm-group/` },
          { label: 'Download tarball with curl:', lang: 'bash', content: `curl -u admin:admin123 \\
  -O "${base}/repository/npm-group/mypackage/-/mypackage-1.0.0.tgz"` },
        ],
      },
    ],
  },
  {
    id: 'pypi',
    name: 'PyPI',
    icon: '🐍',
    description: 'Host Python packages and proxy PyPI. Supports pip, twine, and the PyPI Simple API.',
    sections: (base) => {
      const host = base.replace(/^https?:\/\//, '').split(':')[0]
      return [
        {
          title: 'Repository URLs',
          codes: [{ lang: 'text', content: `${base}/repository/pypi-hosted/\n${base}/repository/pypi-proxy/simple/\n${base}/repository/pypi-group/simple/` }],
        },
        {
          title: 'Configure pip (~/.config/pip/pip.conf)',
          codes: [{ lang: 'ini', content: `[global]
index-url = ${base}/repository/pypi-group/simple/
trusted-host = ${host}` }],
        },
        {
          title: 'Publish a Package',
          codes: [
            { label: 'Using twine:', lang: 'bash', content: `python -m build
twine upload \\
  --repository-url ${base}/repository/pypi-hosted/ \\
  --username admin \\
  --password admin123 \\
  dist/*` },
            { label: 'Using curl:', lang: 'bash', content: `curl -u admin:admin123 \\
  -F "content=@dist/mypackage-1.0.0.tar.gz" \\
  "${base}/repository/pypi-hosted/"` },
          ],
        },
        {
          title: 'Install a Package',
          codes: [{ lang: 'bash', content: `pip install mypackage \\
  --index-url ${base}/repository/pypi-group/simple/ \\
  --trusted-host ${host}` }],
        },
      ]
    },
  },
  {
    id: 'docker',
    name: 'Docker / OCI',
    icon: '🐳',
    description: 'OCI Distribution Spec v2 compliant registry. Supports docker pull/push, image tagging, and multi-arch manifests.',
    sections: (base) => {
      const regHost = base.replace(/^https?:\/\//, '')
      return [
        {
          title: 'Registry Host',
          text: 'Docker uses the host:port directly — no /repository/ prefix. The image name includes the repository.',
          codes: [{ lang: 'text', content: `${regHost}/<repository-name>/<image-name>:<tag>` }],
        },
        {
          title: 'Login',
          codes: [{ lang: 'bash', content: `docker login ${regHost} -u admin -p admin123
# Or with an API token:
docker login ${regHost} -u admin -p nxs_your_token_here` }],
        },
        {
          title: 'Push an Image',
          codes: [{ lang: 'bash', content: `# Tag your local image (docker-hosted is the Nexspence repository name)
docker tag myapp:latest ${regHost}/docker-hosted/myapp:latest

# Push to Nexspence
docker push ${regHost}/docker-hosted/myapp:latest` }],
        },
        {
          title: 'Pull an Image',
          codes: [
            { label: 'Using docker pull:', lang: 'bash', content: `docker pull ${regHost}/docker-hosted/myapp:latest` },
            { label: 'Inspect manifest with curl:', lang: 'bash', content: `curl -u admin:admin123 \\
  "${base}/v2/docker-hosted/myapp/manifests/latest" \\
  -H "Accept: application/vnd.docker.distribution.manifest.v2+json"` },
          ],
        },
        {
          title: 'List Tags',
          codes: [{ lang: 'bash', content: `curl -u admin:admin123 "${base}/v2/myapp/tags/list"` }],
        },
      ]
    },
  },
  {
    id: 'go',
    name: 'Go Modules',
    icon: '🔵',
    description: 'GOPROXY v2 protocol. Cache and proxy Go modules with version resolution and mod file serving.',
    sections: (base) => [
      {
        title: 'Repository URL',
        codes: [{ lang: 'text', content: `${base}/repository/go-proxy/` }],
      },
      {
        title: 'Configure Go Proxy',
        codes: [
          { label: 'Set environment variable:', lang: 'bash', content: `export GOPROXY="${base}/repository/go-proxy/|direct"
export GONOSUMDB="*"    # bypass sum database for all modules (self-signed TLS)` },
          { label: 'Persist with go env -w:', lang: 'bash', content: `go env -w GOPROXY="${base}/repository/go-proxy/|direct"` },
        ],
      },
      {
        title: 'Download a Module',
        codes: [
          { label: 'Using go get:', lang: 'bash', content: `go get github.com/some/module@v1.2.3` },
          { label: 'GOPROXY v2 protocol via curl:', lang: 'bash', content: `# List available versions
curl -u admin:admin123 \\
  "${base}/repository/go-proxy/github.com/some/module/@v/list"

# Download module zip
curl -u admin:admin123 \\
  -O "${base}/repository/go-proxy/github.com/some/module/@v/v1.2.3.zip"

# Fetch go.mod
curl -u admin:admin123 \\
  "${base}/repository/go-proxy/github.com/some/module/@v/v1.2.3.mod"` },
        ],
      },
    ],
  },
  {
    id: 'nuget',
    name: 'NuGet',
    icon: '💜',
    description: 'NuGet v2/v3 repository for .NET packages. Compatible with dotnet CLI, nuget.exe, and MSBuild PackageReference.',
    sections: (base) => [
      {
        title: 'Repository URLs',
        codes: [{ lang: 'text', content: `${base}/repository/nuget-hosted/index.json    ← v3 API\n${base}/repository/nuget-hosted/              ← v2 OData\n${base}/repository/nuget-group/index.json     ← group` }],
      },
      {
        title: 'Configure nuget.config',
        codes: [{ lang: 'xml', content: `<?xml version="1.0" encoding="utf-8"?>
<configuration>
  <packageSources>
    <add key="nexspence" value="${base}/repository/nuget-group/index.json" />
  </packageSources>
  <packageSourceCredentials>
    <nexspence>
      <add key="Username" value="admin" />
      <add key="ClearTextPassword" value="admin123" />
    </nexspence>
  </packageSourceCredentials>
</configuration>` }],
      },
      {
        title: 'Publish a Package',
        codes: [
          { label: 'Using dotnet CLI:', lang: 'bash', content: `dotnet nuget push mypackage.1.0.0.nupkg \\
  --source ${base}/repository/nuget-hosted/ \\
  --api-key admin:admin123` },
          { label: 'Using curl:', lang: 'bash', content: `curl -u admin:admin123 \\
  -F "package=@mypackage.1.0.0.nupkg" \\
  "${base}/repository/nuget-hosted/"` },
        ],
      },
      {
        title: 'Install a Package',
        codes: [{ lang: 'bash', content: `dotnet add package MyPackage \\
  --source ${base}/repository/nuget-group/index.json` }],
      },
    ],
  },
  {
    id: 'raw',
    name: 'Raw',
    icon: '📄',
    description: 'Generic file storage at any path. Ideal for scripts, release tarballs, configuration files, and binary assets.',
    sections: (base) => [
      {
        title: 'Repository URL',
        codes: [{ lang: 'text', content: `${base}/repository/raw-hosted/<path/to/your/file>` }],
      },
      {
        title: 'Upload a File',
        codes: [
          { label: 'Using curl PUT:', lang: 'bash', content: `curl -u admin:admin123 \\
  -T myfile.tar.gz \\
  "${base}/repository/raw-hosted/releases/v1.0/myfile.tar.gz"` },
          { label: 'Upload binary with --data-binary:', lang: 'bash', content: `curl -u admin:admin123 \\
  -X PUT \\
  --data-binary @deploy.sh \\
  "${base}/repository/raw-hosted/scripts/deploy.sh"` },
        ],
      },
      {
        title: 'Download a File',
        codes: [
          { label: 'Using curl:', lang: 'bash', content: `curl -u admin:admin123 \\
  -O "${base}/repository/raw-hosted/releases/v1.0/myfile.tar.gz"` },
          { label: 'Using wget:', lang: 'bash', content: `wget --user=admin --password=admin123 \\
  "${base}/repository/raw-hosted/scripts/deploy.sh"` },
        ],
      },
      {
        title: 'List Files',
        codes: [{ lang: 'bash', content: `curl -u admin:admin123 \\
  "${base}/service/rest/v1/components?repository=raw-hosted" \\
  | python3 -m json.tool` }],
      },
    ],
  },
  {
    id: 'helm',
    name: 'Helm',
    icon: '⚓',
    description: 'Helm chart repository for Kubernetes. Serves Helm charts with auto-generated index.yaml.',
    sections: (base) => [
      {
        title: 'Repository URL',
        codes: [{ lang: 'text', content: `${base}/repository/helm-hosted/` }],
      },
      {
        title: 'Add Repository',
        codes: [{ lang: 'bash', content: `helm repo add nexspence ${base}/repository/helm-hosted/ \\
  --username admin \\
  --password admin123

helm repo update` }],
      },
      {
        title: 'Publish a Chart',
        codes: [
          { label: 'Package then upload with curl:', lang: 'bash', content: `helm package mychart/
curl -u admin:admin123 \\
  -T mychart-1.0.0.tgz \\
  "${base}/repository/helm-hosted/mychart-1.0.0.tgz"` },
          { label: 'Using helm cm-push plugin:', lang: 'bash', content: `helm plugin install https://github.com/chartmuseum/helm-push
helm cm-push mychart/ nexspence` },
        ],
      },
      {
        title: 'Install a Chart',
        codes: [
          { label: 'Using helm install:', lang: 'bash', content: `helm install my-release nexspence/mychart --version 1.0.0` },
          { label: 'Download chart with curl:', lang: 'bash', content: `curl -u admin:admin123 \\
  -O "${base}/repository/helm-hosted/mychart-1.0.0.tgz"` },
        ],
      },
    ],
  },
  {
    id: 'cargo',
    name: 'Cargo (Rust)',
    icon: '🦀',
    description: 'Rust Cargo sparse registry. Supports cargo publish, cargo add, and the sparse index protocol.',
    sections: (base) => [
      {
        title: 'Repository URL',
        codes: [{ lang: 'text', content: `${base}/repository/cargo-hosted/` }],
      },
      {
        title: 'Configure ~/.cargo/config.toml',
        codes: [{ lang: 'toml', content: `[registries.nexspence]
index = "sparse+${base}/repository/cargo-hosted/"
credential-provider = "cargo:token"

[registry]
default = "nexspence"` }],
      },
      {
        title: 'Authenticate',
        codes: [{ lang: 'bash', content: `cargo login --registry nexspence nxs_your_token_here` }],
      },
      {
        title: 'Publish a Crate',
        codes: [
          { label: 'Using cargo publish:', lang: 'bash', content: `cargo publish --registry nexspence` },
        ],
      },
      {
        title: 'Add a Dependency',
        codes: [{ lang: 'bash', content: `cargo add mycrate --registry nexspence` }],
      },
    ],
  },
  {
    id: 'apt',
    name: 'Apt / Debian',
    icon: '🐧',
    description: 'Debian APT repository. Serves .deb packages with auto-generated Packages index.',
    sections: (base) => [
      {
        title: 'Repository URL',
        codes: [{ lang: 'text', content: `${base}/repository/apt-hosted/` }],
      },
      {
        title: 'Configure /etc/apt/sources.list.d/nexspence.list',
        codes: [{ lang: 'bash', content: `echo "deb [trusted=yes] ${base}/repository/apt-hosted/ focal main" \\
  | sudo tee /etc/apt/sources.list.d/nexspence.list

sudo apt-get update` }],
        note: 'Replace "focal main" with your distribution codename and component (e.g. "jammy main", "bullseye contrib").',
      },
      {
        title: 'Publish a .deb Package',
        codes: [{ lang: 'bash', content: `curl -u admin:admin123 \\
  -H "Content-Type: application/octet-stream" \\
  -T mypackage_1.0_amd64.deb \\
  "${base}/repository/apt-hosted/"` }],
      },
      {
        title: 'Install a Package',
        codes: [
          { label: 'Using apt-get:', lang: 'bash', content: `sudo apt-get install mypackage` },
          { label: 'Direct .deb download and install:', lang: 'bash', content: `curl -u admin:admin123 \\
  -O "${base}/repository/apt-hosted/pool/main/m/mypackage/mypackage_1.0_amd64.deb"
sudo dpkg -i mypackage_1.0_amd64.deb` },
        ],
      },
    ],
  },
  {
    id: 'yum',
    name: 'Yum / RPM',
    icon: '🔴',
    description: 'Yum/DNF RPM repository. Serves RPM packages with auto-generated repomd.xml metadata.',
    sections: (base) => [
      {
        title: 'Repository URL',
        codes: [{ lang: 'text', content: `${base}/repository/yum-hosted/` }],
      },
      {
        title: 'Configure /etc/yum.repos.d/nexspence.repo',
        codes: [{ lang: 'ini', content: `[nexspence]
name=Nexspence Repository
baseurl=${base}/repository/yum-hosted/
enabled=1
gpgcheck=0
username=admin
password=admin123` }],
      },
      {
        title: 'Publish an RPM Package',
        codes: [{ lang: 'bash', content: `curl -u admin:admin123 \\
  -H "Content-Type: application/x-rpm" \\
  -T mypackage-1.0-1.x86_64.rpm \\
  "${base}/repository/yum-hosted/"` }],
      },
      {
        title: 'Install a Package',
        codes: [
          { label: 'Using yum/dnf:', lang: 'bash', content: `sudo yum install mypackage
# or
sudo dnf install mypackage` },
          { label: 'Direct RPM download and install:', lang: 'bash', content: `curl -u admin:admin123 \\
  -O "${base}/repository/yum-hosted/mypackage-1.0-1.x86_64.rpm"
sudo rpm -ivh mypackage-1.0-1.x86_64.rpm` },
        ],
      },
    ],
  },
  {
    id: 'conan',
    name: 'Conan C/C++',
    icon: '🔧',
    description: 'Conan v1 package manager repository for C and C++ libraries. Supports upload/download protocol.',
    sections: (base) => [
      {
        title: 'Repository URL',
        codes: [{ lang: 'text', content: `${base}/repository/conan-hosted/` }],
      },
      {
        title: 'Add Remote and Authenticate',
        codes: [{ lang: 'bash', content: `conan remote add nexspence ${base}/repository/conan-hosted/
conan user admin -p admin123 -r nexspence` }],
      },
      {
        title: 'Publish a Package',
        codes: [
          { label: 'Using conan upload:', lang: 'bash', content: `conan upload mylib/1.0@user/stable -r nexspence --all` },
          { label: 'Get upload URLs with curl:', lang: 'bash', content: `curl -u admin:admin123 \\
  "${base}/repository/conan-hosted/v1/conans/mylib/1.0/user/stable/upload_urls"` },
        ],
      },
      {
        title: 'Install a Package',
        codes: [
          { label: 'Using conan install:', lang: 'bash', content: `conan install mylib/1.0@user/stable -r nexspence` },
          { label: 'Get download URLs with curl:', lang: 'bash', content: `curl -u admin:admin123 \\
  "${base}/repository/conan-hosted/v1/conans/mylib/1.0/user/stable/download_urls"` },
        ],
      },
    ],
  },
]

function GuideRepositories() {
  return (
    <>
      <div className={styles.sectionHeader}>
        <h1 className={styles.sectionTitle}>Creating Repositories</h1>
        <p className={styles.sectionDesc}>Step-by-step guide to creating Hosted, Proxy, and Group repositories for any supported format.</p>
      </div>
    </>
  )
}
function GuideUsers() {
  return (
    <>
      <div className={styles.sectionHeader}>
        <h1 className={styles.sectionTitle}>Managing Users</h1>
        <p className={styles.sectionDesc}>Create local user accounts, assign roles, and manage API token access.</p>
      </div>
    </>
  )
}
function GuideRolesPrivileges() {
  return (
    <>
      <div className={styles.sectionHeader}>
        <h1 className={styles.sectionTitle}>Roles &amp; Privileges</h1>
        <p className={styles.sectionDesc}>Set up RBAC with Content Selectors → Privileges → Roles → Users.</p>
      </div>
    </>
  )
}
function GuideContentSelectors() {
  return (
    <>
      <div className={styles.sectionHeader}>
        <h1 className={styles.sectionTitle}>Content Selectors</h1>
        <p className={styles.sectionDesc}>Write CEL expressions to scope artifact path access for privileges.</p>
      </div>
    </>
  )
}
function GuideSecurityScanning() {
  return (
    <>
      <div className={styles.sectionHeader}>
        <h1 className={styles.sectionTitle}>Security Scanning</h1>
        <p className={styles.sectionDesc}>Scan artifacts for CVE vulnerabilities using the OSV database.</p>
      </div>
    </>
  )
}
function GuideCleanupPolicies() {
  return (
    <>
      <div className={styles.sectionHeader}>
        <h1 className={styles.sectionTitle}>Cleanup Policies</h1>
        <p className={styles.sectionDesc}>Automate artifact retention with scheduled cleanup rules.</p>
      </div>
    </>
  )
}
function GuideApiTokens() {
  return (
    <>
      <div className={styles.sectionHeader}>
        <h1 className={styles.sectionTitle}>API Tokens</h1>
        <p className={styles.sectionDesc}>Generate nxs_* tokens and use them for Basic Auth or Bearer authentication.</p>
      </div>
    </>
  )
}

function GettingStarted({ base }: { base: string }) {
  return (
    <>
      <div className={styles.sectionHeader}>
        <h1 className={styles.sectionTitle}>Getting Started</h1>
        <p className={styles.sectionDesc}>
          Learn how to authenticate and connect your build tools to Nexspence.
        </p>
      </div>

      <p className={styles.blockTitle}>Your Base URL</p>
      <UrlBlock url={base} />

      <p className={styles.blockTitle}>Authentication</p>
      <p className={styles.blockText}>Three methods are supported — use any one:</p>
      <CodeBlock lang="bash" content={`# 1. Username + password
curl -u admin:admin123 "${base}/api/v1/repositories"

# 2. API token as password (get from Profile → API Tokens)
curl -u admin:nxs_your_token_here "${base}/api/v1/repositories"

# 3. Bearer token
curl -H "Authorization: Bearer nxs_your_token_here" \\
  "${base}/api/v1/repositories"`} />

      <p className={styles.blockTitle}>Generate an API Token</p>
      <p className={styles.blockText}>
        Click the key icon in the sidebar → <strong style={{ color: 'var(--holo-text)' }}>API Tokens</strong> → Create Token.
        Tokens start with <span className={styles.inlineCode}>nxs_</span> and work as a password in Basic Auth
        or as a Bearer token header.
      </p>

      <p className={styles.blockTitle}>List Repositories</p>
      <CodeBlock lang="bash" content={`curl -u admin:admin123 \\
  "${base}/service/rest/v1/repositories" | python3 -m json.tool`} />

      <p className={styles.blockTitle}>Nexus API Compatibility</p>
      <p className={styles.blockText}>
        Nexspence is a drop-in replacement for Sonatype Nexus OSS.
        All Nexus REST API endpoints under <span className={styles.inlineCode}>/service/rest/v1/</span> are supported.
        Tools already configured for Nexus work without modification.
      </p>

      <p className={styles.blockTitle}>Browse & Search</p>
      <p className={styles.blockText}>
        Use the <strong style={{ color: 'var(--holo-text)' }}>Browse</strong> and <strong style={{ color: 'var(--holo-text)' }}>Search</strong> pages
        in the sidebar to explore artifacts visually, or use the Nexus REST API:
      </p>
      <CodeBlock lang="bash" content={`# Search by name
curl -u admin:admin123 \\
  "${base}/service/rest/v1/search?name=myapp" | python3 -m json.tool

# List assets in a repository
curl -u admin:admin123 \\
  "${base}/service/rest/v1/components?repository=maven-releases"`} />
    </>
  )
}

function FormatContent({ format, base }: { format: Format; base: string }) {
  const sections = format.sections(base)
  return (
    <>
      <div className={styles.sectionHeader}>
        <h1 className={styles.sectionTitle}>{format.icon} {format.name}</h1>
        <p className={styles.sectionDesc}>{format.description}</p>
      </div>
      {sections.map((s, i) => (
        <div key={i}>
          <SectionBlock section={s} />
          {i < sections.length - 1 && <hr className={styles.divider} />}
        </div>
      ))}
    </>
  )
}

export default function DocsPage() {
  const [active, setActive] = useState('getting-started')
  const base = window.location.origin

  return (
    <div className={styles.docsLayout}>
      <nav className={styles.docsNav}>
        <button
          className={`${styles.docsNavBtn} ${active === 'getting-started' ? styles.active : ''}`}
          onClick={() => setActive('getting-started')}
        >
          <BookOpen size={14} style={{ flexShrink: 0 }} />
          Getting Started
        </button>

        <div className={styles.docsNavSection}>Guides</div>
        {([
          { id: 'guide-repos',     label: '🗄 Creating Repositories' },
          { id: 'guide-users',     label: '👥 Managing Users' },
          { id: 'guide-roles',     label: '🛡 Roles & Privileges' },
          { id: 'guide-selectors', label: '🔍 Content Selectors' },
          { id: 'guide-security',  label: '🔐 Security Scanning' },
          { id: 'guide-cleanup',   label: '🗑 Cleanup Policies' },
          { id: 'guide-tokens',    label: '🔑 API Tokens' },
        ] as { id: string; label: string }[]).map(g => (
          <button
            key={g.id}
            className={`${styles.docsNavBtn} ${active === g.id ? styles.active : ''}`}
            onClick={() => setActive(g.id)}
          >
            {g.label}
          </button>
        ))}

        <div className={styles.docsNavSection}>Formats</div>
        {FORMATS.map(f => (
          <button
            key={f.id}
            className={`${styles.docsNavBtn} ${active === f.id ? styles.active : ''}`}
            onClick={() => setActive(f.id)}
          >
            {f.iconUrl
              ? <img src={f.iconUrl} alt="" width={14} height={14} className={styles.navBrandIcon} onError={(e) => { (e.currentTarget as HTMLImageElement).style.display = 'none' }} />
              : <span style={{ fontSize: 14, lineHeight: 1, flexShrink: 0 }}>{f.icon}</span>
            }
            {f.name}
          </button>
        ))}
      </nav>

      <div className={styles.docsContent}>
        {active === 'getting-started' && <GettingStarted base={base} />}
        {active === 'guide-repos'     && <GuideRepositories />}
        {active === 'guide-users'     && <GuideUsers />}
        {active === 'guide-roles'     && <GuideRolesPrivileges />}
        {active === 'guide-selectors' && <GuideContentSelectors />}
        {active === 'guide-security'  && <GuideSecurityScanning />}
        {active === 'guide-cleanup'   && <GuideCleanupPolicies />}
        {active === 'guide-tokens'    && <GuideApiTokens />}
        {FORMATS.map(f => active === f.id && <FormatContent key={f.id} format={f} base={base} />)}
      </div>
    </div>
  )
}
