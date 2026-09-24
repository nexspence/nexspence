/**
 * Client setup snippets — the one source for both the in-app docs (DocsPage)
 * and the per-repository Set Me Up dialog (#534), so the two cannot drift.
 *
 * The builders take everything they print as arguments (URL, repository name,
 * username, secret placeholder) and hold no state. DocsPage calls them with its
 * generic example names; `buildSetupGuide` composes them into per-client tabs
 * for one concrete repository.
 *
 * Every URL shape here was checked against the server's format handlers
 * (internal/formats/<format>/handler.go). Secrets are never rendered: the Set
 * Me Up guide always prints SECRET_PLACEHOLDER where a password or token goes.
 */

export interface Snippet { label?: string; lang: string; content: string }
export interface SetupSection { title: string; text?: string; note?: string; codes: Snippet[] }
export interface SetupClient { id: string; label: string; sections: SetupSection[] }

export interface SetupContext {
  format: string
  repoName: string
  repoType: string
  /** Server origin, e.g. https://nexspence.example.com — a trailing slash is ignored. */
  baseUrl: string
  /** Optional; USERNAME_PLACEHOLDER is printed when empty. */
  username?: string
}

export interface SetupGuide {
  repoUrl: string
  /** Shown above the tabs, e.g. where pushes go for a proxy or group. */
  note?: string
  clients: SetupClient[]
}

export const SECRET_PLACEHOLDER = '<your-token>'
export const USERNAME_PLACEHOLDER = '<your-username>'

// ── URL helpers ───────────────────────────────────────────────

export function trimBase(base: string): string {
  return base.replace(/\/+$/, '')
}

/** host[:port] of a base URL — what docker and .npmrc keys want. */
export function hostOf(base: string): string {
  return trimBase(base).replace(/^https?:\/\//, '')
}

/** host without the port — pip's trusted-host and .netrc take a bare hostname. */
export function hostnameOf(base: string): string {
  return hostOf(base).split(':')[0]
}

/** The repository's client URL, with a trailing slash. */
export function repoUrl(base: string, name: string): string {
  return `${trimBase(base)}/repository/${name}/`
}

/** uv reads index credentials from UV_INDEX_<NAME>_USERNAME: the name upper-cased, non-alphanumerics as _. */
export function envName(name: string): string {
  return name.toUpperCase().replace(/[^A-Z0-9]/g, '_')
}

// ── Builders (shared with DocsPage) ───────────────────────────

export const maven = {
  settingsServer: (serverId: string, user: string, secret: string) => `<settings>
  <servers>
    <server>
      <id>${serverId}</id>
      <username>${user}</username>
      <password>${secret}</password>
    </server>
  </servers>
</settings>`,

  settingsMirror: (serverId: string, url: string, user: string, secret: string) => `<settings>
  <servers>
    <server>
      <id>${serverId}</id>
      <username>${user}</username>
      <password>${secret}</password>
    </server>
  </servers>
  <mirrors>
    <mirror>
      <id>${serverId}</id>
      <url>${url}</url>
      <mirrorOf>*</mirrorOf>
    </mirror>
  </mirrors>
</settings>`,

  pomRepository: (serverId: string, url: string) => `<repositories>
  <repository>
    <id>${serverId}</id>
    <url>${url}</url>
  </repository>
</repositories>`,

  pomDistribution: (serverId: string, url: string) => `<distributionManagement>
  <repository>
    <id>${serverId}</id>
    <url>${url}</url>
  </repository>
  <snapshotRepository>
    <id>${serverId}</id>
    <url>${url}</url>
  </snapshotRepository>
</distributionManagement>`,

  deployFile: (serverId: string, url: string) => `mvn deploy:deploy-file \\
  -DrepositoryId=${serverId} \\
  -Durl=${url} \\
  -Dfile=myapp-1.0.jar \\
  -DgroupId=com.example \\
  -DartifactId=myapp \\
  -Dversion=1.0`,

  gradleRepository: (url: string) => `repositories {
    maven {
        url = uri("${url}")
        credentials {
            username = providers.gradleProperty("nexspenceUsername").get()
            password = providers.gradleProperty("nexspencePassword").get()
        }
    }
}`,

  gradlePublishing: (url: string) => `plugins {
    \`maven-publish\`
}

publishing {
    publications {
        create<MavenPublication>("maven") {
            from(components["java"])
        }
    }
    repositories {
        maven {
            name = "nexspence"
            url = uri("${url}")
            credentials {
                username = providers.gradleProperty("nexspenceUsername").get()
                password = providers.gradleProperty("nexspencePassword").get()
            }
        }
    }
}`,

  gradleProperties: (user: string, secret: string) => `nexspenceUsername=${user}
nexspencePassword=${secret}`,
}

export const npm = {
  /** .npmrc for one registry, with an auth token keyed to that registry's path. */
  npmrc: (url: string, secret: string, scope?: string) => {
    const registryLine = scope ? `${scope}:registry=${url}` : `registry=${url}`
    return `${registryLine}\n//${url.replace(/^https?:\/\//, '')}:_authToken=${secret}`
  },
  publish: (url: string, tool = 'npm') => `${tool} publish --registry ${url}`,
  install: (url: string, tool = 'npm') => `${tool} install mypackage --registry ${url}`,
  yarnrc: (url: string, secret: string) => `npmRegistryServer: "${url}"
npmAlwaysAuth: true
npmAuthToken: "${secret}"`,
  yarnrcScoped: (url: string, secret: string, scope: string) => `npmScopes:
  ${scope.replace(/^@/, '')}:
    npmRegistryServer: "${url}"
    npmAlwaysAuth: true
    npmAuthToken: "${secret}"`,
}

export const pypi = {
  pipConf: (indexUrl: string, hostname: string) => `[global]
index-url = ${indexUrl}
trusted-host = ${hostname}`,
  netrc: (hostname: string, user: string, secret: string) => `machine ${hostname}
login ${user}
password ${secret}`,
  /** Uploads are a multipart POST to the repository root (no /legacy/). */
  twineUpload: (uploadUrl: string, user: string, secret: string) => `python -m build
twine upload \\
  --repository-url ${uploadUrl} \\
  --username ${user} \\
  --password ${secret} \\
  dist/*`,
  /** The server requires name and version form fields next to content. */
  curlUpload: (uploadUrl: string, user: string, secret: string) => `curl -u ${user}:${secret} \\
  -F "name=mypackage" \\
  -F "version=1.0.0" \\
  -F "content=@dist/mypackage-1.0.0.tar.gz" \\
  "${uploadUrl}"`,
  pypirc: (name: string, uploadUrl: string, user: string) => `[distutils]
index-servers = ${name}

[${name}]
repository = ${uploadUrl}
username = ${user}`,
  uvIndex: (name: string, indexUrl: string) => `[[tool.uv.index]]
name = "${name}"
url = "${indexUrl}"
default = true`,
}

export const docker = {
  login: (host: string, user: string, secret: string) => `docker login ${host} -u ${user} -p ${secret}`,
  /** Tags live under the repository name, the first path segment after /v2/. */
  tagsList: (base: string, repo: string, image: string, user: string, secret: string) =>
    `curl -u ${user}:${secret} "${trimBase(base)}/v2/${repo}/${image}/tags/list"`,
}

export const helm = {
  repoAdd: (alias: string, url: string, user: string, secret: string) => `helm repo add ${alias} ${url} \\
  --username ${user} \\
  --password ${secret}

helm repo update`,
  /** A chart is uploaded with a PUT of the packaged .tgz at the repository root. */
  curlUpload: (url: string, user: string, secret: string) => `helm package mychart/
curl -u ${user}:${secret} \\
  -T mychart-1.0.0.tgz \\
  "${url}mychart-1.0.0.tgz"`,
}

export const nuget = {
  config: (key: string, indexUrl: string, user: string, secret: string) => `<?xml version="1.0" encoding="utf-8"?>
<configuration>
  <packageSources>
    <add key="${key}" value="${indexUrl}" />
  </packageSources>
  <packageSourceCredentials>
    <${key}>
      <add key="Username" value="${user}" />
      <add key="ClearTextPassword" value="${secret}" />
    </${key}>
  </packageSourceCredentials>
</configuration>`,
  addSource: (key: string, indexUrl: string, user: string, secret: string) => `dotnet nuget add source ${indexUrl} \\
  --name ${key} \\
  --username ${user} \\
  --password ${secret} \\
  --store-password-in-clear-text`,
  /**
   * The push target must be the v3 index.json (it advertises the publish
   * endpoint). The server ignores the API key and authenticates with the
   * source's credentials, but the client insists on having one.
   */
  push: (source: string) => `dotnet nuget push mypackage.1.0.0.nupkg \\
  --source ${source} \\
  --api-key unused`,
  curlUpload: (url: string, user: string, secret: string) => `curl -u ${user}:${secret} \\
  -X PUT \\
  -F "package=@mypackage.1.0.0.nupkg" \\
  "${url}v2/package"`,
}

export const cargo = {
  /** The sparse index lives under index/ — config.json is served only there. */
  indexUrl: (url: string) => `${url}index/`,
  registryConfig: (name: string, indexUrl: string) => `[registries.${name}]
index = "sparse+${indexUrl}"
credential-provider = "cargo:token"`,
  /**
   * cargo sends the token verbatim as the Authorization header, and the server
   * only understands "Bearer …" and "Basic …" — a bare token is anonymous.
   */
  login: (name: string, secret: string) => `cargo login --registry ${name} "Bearer ${secret}"`,
}

// ── Set Me Up composition ─────────────────────────────────────

const tab = (id: string, label: string, sections: SetupSection[]): SetupClient => ({ id, label, sections })
const code = (lang: string, content: string, label?: string): Snippet => (label ? { label, lang, content } : { lang, content })

interface Ctx {
  base: string
  host: string
  hostname: string
  name: string
  url: string
  user: string
  secret: string
  hosted: boolean
  type: string
}

/** Where to send the reader of a proxy or group who wants to publish. */
function readOnlyNote(type: string): string | undefined {
  if (type === 'proxy') return 'This is a proxy repository: it serves content cached from its remote and rejects uploads. To publish, use a hosted repository of the same format.'
  if (type === 'group') return 'This is a group repository: reads resolve across its members. Uploads belong in a hosted repository — a push sent to the group is forwarded to its writable member (by default its first hosted member), so publish to that hosted repository directly.'
  return undefined
}

export function buildSetupGuide(input: SetupContext): SetupGuide {
  const base = trimBase(input.baseUrl)
  const c: Ctx = {
    base,
    host: hostOf(base),
    hostname: hostnameOf(base),
    name: input.repoName,
    url: repoUrl(base, input.repoName),
    user: input.username?.trim() || USERNAME_PLACEHOLDER,
    secret: SECRET_PLACEHOLDER,
    hosted: input.repoType === 'hosted',
    type: input.repoType,
  }
  const builder = BUILDERS[input.format.toLowerCase()] ?? rawClients
  return { repoUrl: c.url, note: readOnlyNote(c.type), clients: builder(c) }
}

function curlGet(c: Ctx, path: string): string {
  return `curl -u ${c.user}:${c.secret} \\\n  -O "${c.url}${path}"`
}

function curlPut(c: Ctx, file: string, path: string): string {
  return `curl -u ${c.user}:${c.secret} \\\n  -T ${file} \\\n  "${c.url}${path}"`
}

function rawClients(c: Ctx): SetupClient[] {
  const sections: SetupSection[] = [{ title: 'Download a file', codes: [
    code('bash', curlGet(c, 'path/to/myfile.tar.gz'), 'curl:'),
    code('bash', `wget --user=${c.user} --ask-password \\\n  "${c.url}path/to/myfile.tar.gz"`, 'wget:'),
  ] }]
  if (c.hosted) sections.push({ title: 'Upload a file', text: 'Any path works; directories are created on the fly.', codes: [code('bash', curlPut(c, 'myfile.tar.gz', 'path/to/myfile.tar.gz'))] })
  return [tab('curl', 'curl', sections)]
}

function ociRef(c: Ctx, image: string): string {
  return `${c.host}/${c.name}/${image}`
}

const DOCKER_HOST_NOTE = 'Images are addressed as host/<repository>/<image>. Docker requires HTTPS unless the host is listed under insecure-registries in /etc/docker/daemon.json. If your administrator enabled the subdomain connector, <repository>.<base-domain>/<image> works as well.'

const BUILDERS: Record<string, (c: Ctx) => SetupClient[]> = {
  maven2: (c) => {
    const mvn: SetupSection[] = c.hosted
      ? [
          { title: 'Credentials — ~/.m2/settings.xml', codes: [code('xml', maven.settingsServer(c.name, c.user, c.secret))] },
          { title: 'Resolve from this repository — pom.xml', codes: [code('xml', maven.pomRepository(c.name, c.url))] },
          { title: 'Deploy — pom.xml', text: 'The <id> must match the server id in settings.xml.', codes: [code('xml', maven.pomDistribution(c.name, c.url)), code('bash', 'mvn deploy', 'Then:')] },
          { title: 'Deploy a single file', codes: [code('bash', maven.deployFile(c.name, c.url))] },
        ]
      : [{ title: 'Mirror everything through this repository — ~/.m2/settings.xml', codes: [code('xml', maven.settingsMirror(c.name, c.url, c.user, c.secret))] }]
    const gradle: SetupSection[] = [
      { title: 'Credentials — ~/.gradle/gradle.properties', codes: [code('properties', maven.gradleProperties(c.user, c.secret))] },
      { title: 'Resolve — build.gradle.kts', codes: [code('kotlin', maven.gradleRepository(c.url))] },
    ]
    if (c.hosted) gradle.push({ title: 'Publish — build.gradle.kts', codes: [code('kotlin', maven.gradlePublishing(c.url)), code('bash', './gradlew publish', 'Then:')] })
    return [tab('maven', 'Maven', mvn), tab('gradle', 'Gradle', gradle)]
  },

  npm: (c) => {
    const text = 'npm login is not supported — put an API token in .npmrc. A scoped entry routes only @myscope packages here and leaves the rest on your default registry.'
    const tool = (id: 'npm' | 'pnpm'): SetupClient => {
      const sections: SetupSection[] = [
        { title: 'Configure .npmrc', text, codes: [
          code('ini', npm.npmrc(c.url, c.secret), 'All packages:'),
          code('ini', npm.npmrc(c.url, c.secret, '@myscope'), 'One scope only:'),
        ] },
        { title: 'Install', codes: [code('bash', id === 'npm' ? npm.install(c.url) : `pnpm add mypackage --registry ${c.url}`)] },
      ]
      if (c.hosted) sections.push({ title: 'Publish', codes: [code('bash', npm.publish(c.url, id))] })
      return tab(id, id, sections)
    }
    const yarn: SetupSection[] = [
      { title: 'Configure .yarnrc.yml (Yarn 2+)', codes: [
        code('yaml', npm.yarnrc(c.url, c.secret), 'All packages:'),
        code('yaml', npm.yarnrcScoped(c.url, c.secret, '@myscope'), 'One scope only:'),
      ] },
      { title: 'Install', codes: [code('bash', 'yarn add mypackage')] },
    ]
    if (c.hosted) yarn.push({ title: 'Publish', codes: [code('bash', 'yarn npm publish')] })
    return [tool('npm'), tool('pnpm'), tab('yarn', 'yarn', yarn)]
  },

  pypi: (c) => {
    const index = `${c.url}simple/`
    const netrc: SetupSection = { title: 'Credentials — ~/.netrc', text: 'pip and uv read credentials for this host from ~/.netrc.', codes: [code('text', pypi.netrc(c.hostname, c.user, c.secret))] }
    const pip: SetupSection[] = [
      { title: 'Configure pip — ~/.config/pip/pip.conf', codes: [code('ini', pypi.pipConf(index, c.hostname))] },
      netrc,
      { title: 'Install', codes: [code('bash', `pip install mypackage --index-url ${index}`)] },
    ]
    if (c.hosted) pip.push({ title: 'Upload with twine', codes: [
      code('bash', pypi.twineUpload(c.url, c.user, c.secret)),
      code('ini', pypi.pypirc(c.name, c.url, c.user), `Or keep it in ~/.pypirc and run twine upload -r ${c.name} dist/*:`),
    ] })
    const uv: SetupSection[] = [
      { title: 'Configure — pyproject.toml', codes: [code('toml', pypi.uvIndex(c.name, index))] },
      { title: 'Credentials', codes: [code('bash', `export UV_INDEX_${envName(c.name)}_USERNAME=${c.user}\nexport UV_INDEX_${envName(c.name)}_PASSWORD=${c.secret}`)] },
    ]
    if (c.hosted) uv.push({ title: 'Publish', codes: [code('bash', `uv build\nuv publish --publish-url ${c.url} \\\n  --username ${c.user} --password ${c.secret}`)] })
    const poetry: SetupSection[] = [
      { title: 'Add the source', codes: [code('bash', `poetry source add --priority=primary ${c.name} ${index}\npoetry config http-basic.${c.name} ${c.user} ${c.secret}`)] },
    ]
    if (c.hosted) poetry.push({ title: 'Publish', codes: [code('bash', `poetry config repositories.${c.name} ${c.url}\npoetry config http-basic.${c.name} ${c.user} ${c.secret}\npoetry publish --build -r ${c.name}`)] })
    return [tab('pip', 'pip', pip), tab('uv', 'uv', uv), tab('poetry', 'poetry', poetry)]
  },

  docker: (c) => {
    const pullImage = c.type === 'proxy' ? 'library/nginx:latest' : 'myapp:1.0'
    const sections: SetupSection[] = [
      { title: 'Log in', text: DOCKER_HOST_NOTE, codes: [code('bash', docker.login(c.host, c.user, c.secret))] },
      { title: 'Pull', codes: [code('bash', `docker pull ${ociRef(c, pullImage)}`)] },
    ]
    if (c.hosted) sections.push({ title: 'Tag and push', codes: [code('bash', `docker tag myapp:1.0 ${ociRef(c, 'myapp:1.0')}\ndocker push ${ociRef(c, 'myapp:1.0')}`)] })
    sections.push({ title: 'List tags', codes: [code('bash', docker.tagsList(c.base, c.name, pullImage.split(':')[0], c.user, c.secret))] })
    const compose: SetupSection[] = [{ title: 'docker-compose.yml', codes: [code('yaml', `services:\n  app:\n    image: ${ociRef(c, pullImage)}`)] }]
    return [tab('docker', 'docker', sections), tab('compose', 'Compose', compose)]
  },

  oci: (c) => {
    const helmSections: SetupSection[] = [
      { title: 'Log in', codes: [code('bash', `helm registry login ${c.host} -u ${c.user} -p ${c.secret}`)] },
      { title: 'Install a chart', codes: [code('bash', `helm install my-release \\\n  oci://${c.host}/${c.name}/charts/mychart --version 1.2.3`)] },
    ]
    if (c.hosted) helmSections.push({ title: 'Push a chart', text: 'helm push takes a packaged chart and the namespace it goes into — not the full reference.', codes: [code('bash', `helm package mychart/\nhelm push mychart-1.2.3.tgz oci://${c.host}/${c.name}/charts`)] })
    const orasSections: SetupSection[] = [
      { title: 'Log in', codes: [code('bash', `oras login ${c.host} -u ${c.user} -p ${c.secret}`)] },
      { title: 'Pull', codes: [code('bash', `oras pull ${ociRef(c, 'my-module:1.0')}`)] },
    ]
    if (c.hosted) orasSections.push({ title: 'Push', codes: [code('bash', `oras push ${ociRef(c, 'my-module:1.0')} \\\n  --artifact-type application/vnd.wasm.config.v1+json \\\n  module.wasm:application/vnd.wasm.content.layer.v1+wasm`)] })
    return [tab('helm', 'helm', helmSections), tab('oras', 'oras', orasSections), ...BUILDERS.docker(c).slice(0, 1)]
  },

  go: (c) => {
    const sections: SetupSection[] = [
      { title: 'Point GOPROXY at this repository', codes: [code('bash', `go env -w GOPROXY="${c.url}|direct"`)] },
      { title: 'Credentials — ~/.netrc', text: 'The go command sends credentials from ~/.netrc for GOPROXY hosts.', codes: [code('text', pypi.netrc(c.hostname, c.user, c.secret))] },
      { title: 'Download a module', codes: [code('bash', 'go get github.com/some/module@v1.2.3')] },
    ]
    if (c.hosted) sections.push({ title: 'Upload a module version', text: 'Hosted Go repositories take a PUT of the .mod and .zip files of each version (a Nexspence extension to the GOPROXY protocol).', codes: [code('bash', `${curlPut(c, 'go.mod', 'example.com/mymod/@v/v1.0.0.mod')}\n\n${curlPut(c, 'v1.0.0.zip', 'example.com/mymod/@v/v1.0.0.zip')}`)] })
    return [tab('go', 'go', sections)]
  },

  nuget: (c) => {
    const index = `${c.url}index.json`
    const dotnet: SetupSection[] = [
      { title: 'Add the source', codes: [code('bash', nuget.addSource(c.name, index, c.user, c.secret))] },
      { title: 'Install', codes: [code('bash', `dotnet add package MyPackage --source ${c.name}`)] },
    ]
    if (c.hosted) dotnet.push({ title: 'Push', text: 'Push authenticates with the source credentials above; the API key is ignored by the server.', codes: [code('bash', nuget.push(c.name))] })
    const config: SetupSection[] = [{ title: 'nuget.config', codes: [code('xml', nuget.config(c.name, index, c.user, c.secret))] }]
    if (c.hosted) config.push({ title: 'Push with nuget.exe', codes: [code('bash', `nuget push mypackage.1.0.0.nupkg -Source ${c.name} -ApiKey unused`)] })
    return [tab('dotnet', 'dotnet', dotnet), tab('nugetconfig', 'nuget.config', config)]
  },

  helm: (c) => {
    const sections: SetupSection[] = [
      { title: 'Add the repository', codes: [code('bash', helm.repoAdd(c.name, c.url, c.user, c.secret))] },
      { title: 'Install a chart', codes: [code('bash', `helm install my-release ${c.name}/mychart --version 1.0.0`)] },
    ]
    if (c.hosted) sections.push({ title: 'Upload a chart', codes: [code('bash', helm.curlUpload(c.url, c.user, c.secret))] })
    return [tab('helm', 'helm', sections)]
  },

  cargo: (c) => {
    const index = cargo.indexUrl(c.url)
    const sections: SetupSection[] = [
      { title: 'Configure — ~/.cargo/config.toml', codes: [code('toml', cargo.registryConfig(c.name, index))] },
      { title: 'Add a dependency', codes: [code('bash', `cargo add mycrate --registry ${c.name}`)] },
    ]
    if (c.hosted) sections.push(
      { title: 'Log in', text: 'Store the token with its Bearer prefix: cargo sends it verbatim.', codes: [code('bash', cargo.login(c.name, c.secret))] },
      { title: 'Publish', codes: [code('bash', `cargo publish --registry ${c.name}`)] },
    )
    return [tab('cargo', 'cargo', sections)]
  },

  rubygems: (c) => {
    const source = c.url.replace(/\/$/, '')
    const bundler: SetupSection[] = [{ title: 'Gemfile', codes: [code('ruby', `source "${source}" do\n  gem "rails"\nend`)] }]
    const gem: SetupSection[] = [{ title: 'Install', codes: [code('bash', `gem install rails --source ${c.url}`)] }]
    if (c.hosted) gem.push({ title: 'Push', text: 'The gem client sends its API key as the Authorization header: store your credentials as HTTP Basic.', codes: [code('bash', `printf ':rubygems_api_key: Basic %s\\n' "$(printf '${c.user}:${c.secret}' | base64)" > ~/.gem/credentials\nchmod 600 ~/.gem/credentials\ngem push my-gem-1.0.0.gem --host ${source}`)] })
    return [tab('bundler', 'Bundler', bundler), tab('gem', 'gem', gem)]
  },

  conan: (c) => {
    const sections: SetupSection[] = [
      { title: 'Add the remote and log in (Conan 2)', codes: [code('bash', `conan remote add ${c.name} ${c.url}\nconan remote login ${c.name} ${c.user} -p ${c.secret}`)] },
      { title: 'Install', codes: [code('bash', `conan install --requires="mylib/1.0" -r=${c.name}`)] },
    ]
    if (c.hosted) sections.push({ title: 'Upload', codes: [code('bash', `conan create .\nconan upload "mylib/1.0" -r=${c.name} --confirm`)] })
    return [tab('conan', 'conan', sections)]
  },

  apt: (c) => {
    const sections: SetupSection[] = [
      { title: 'Add the source', text: 'Any distribution codename works; the component is always main.', codes: [code('bash', `echo "deb [trusted=yes] ${c.url} stable main" \\\n  | sudo tee /etc/apt/sources.list.d/${c.name}.list`)] },
      { title: 'Credentials — /etc/apt/auth.conf.d/' + c.name + '.conf', codes: [code('text', `machine ${c.host}/repository/${c.name}/\nlogin ${c.user}\npassword ${c.secret}`)] },
      { title: 'Install', codes: [code('bash', 'sudo apt-get update\nsudo apt-get install mypackage')] },
    ]
    if (c.hosted) sections.push({ title: 'Upload a .deb', codes: [code('bash', `curl -u ${c.user}:${c.secret} \\\n  -T mypackage_1.0_amd64.deb \\\n  "${c.url}"`)] })
    return [tab('apt', 'apt', sections)]
  },

  yum: (c) => {
    const sections: SetupSection[] = [
      { title: `Repository file — /etc/yum.repos.d/${c.name}.repo`, codes: [code('ini', `[${c.name}]\nname=${c.name}\nbaseurl=${c.url}\nenabled=1\ngpgcheck=0\nusername=${c.user}\npassword=${c.secret}`)] },
      { title: 'Install', codes: [code('bash', 'sudo dnf install mypackage')] },
    ]
    if (c.hosted) sections.push({ title: 'Upload an .rpm', codes: [code('bash', curlPut(c, 'mypackage-1.0-1.x86_64.rpm', 'mypackage-1.0-1.x86_64.rpm'))] })
    return [tab('dnf', 'dnf / yum', sections)]
  },

  alpine: (c) => {
    const sections: SetupSection[] = [
      { title: 'Add the repository', note: 'The index is unsigned, so apk needs --allow-untrusted.', codes: [code('bash', `echo "${c.url.replace(/\/$/, '')}" | sudo tee -a /etc/apk/repositories\nsudo apk update --allow-untrusted`)] },
      { title: 'Install', codes: [code('bash', 'sudo apk add --allow-untrusted mypackage')] },
    ]
    if (c.hosted) sections.push({ title: 'Upload an .apk', text: 'Upload under the package architecture.', codes: [code('bash', curlPut(c, 'mypackage-1.0.0-r0.apk', 'x86_64/mypackage-1.0.0-r0.apk'))] })
    return [tab('apk', 'apk', sections)]
  },

  cran: (c) => {
    const sections: SetupSection[] = [
      { title: 'Use as a repository — ~/.Rprofile', codes: [code('r', `options(repos = c(${rIdent(c.name)} = "${c.url}"))`)] },
      { title: 'Install', codes: [code('r', `install.packages("mypackage", repos = "${c.url}")`)] },
    ]
    if (c.hosted) sections.push({ title: 'Upload a source package', codes: [code('bash', curlPut(c, 'mypackage_1.0.0.tar.gz', 'src/contrib/mypackage_1.0.0.tar.gz'))] })
    return [tab('r', 'R', sections)]
  },

  conda: (c) => {
    const sections: SetupSection[] = [
      { title: 'Add the channel — ~/.condarc', codes: [code('yaml', `channels:\n  - ${c.url}\n  - defaults`)] },
      { title: 'Install', codes: [code('bash', `conda install mypackage \\\n  -c ${c.url} \\\n  --override-channels`)] },
    ]
    if (c.hosted) sections.push({ title: 'Upload a package', text: 'Upload under its platform subdirectory (linux-64, osx-arm64, noarch, …).', codes: [code('bash', curlPut(c, 'mypackage-1.0.0-py311_0.conda', 'linux-64/mypackage-1.0.0-py311_0.conda'))] })
    return [tab('conda', 'conda', sections)]
  },

  terraform: (c) => {
    const sections: SetupSection[] = [
      { title: 'Registry API', text: 'This repository serves the Terraform registry protocol under its own URL. terraform init discovers a registry at https://<host>/.well-known/terraform.json, which Nexspence answers per repository — expose it at the host root with a reverse-proxy rewrite before using the host in a source address.', codes: [
        code('bash', `curl -u ${c.user}:${c.secret} "${c.url}.well-known/terraform.json"`, 'Service discovery:'),
        code('bash', `curl -u ${c.user}:${c.secret} "${c.url}v1/providers/myorg/myprovider/versions"`, 'Provider versions:'),
        code('bash', `curl -u ${c.user}:${c.secret} "${c.url}v1/modules/myorg/vpc/aws/versions"`, 'Module versions:'),
      ] },
    ]
    if (c.hosted) sections.push(
      { title: 'Publish a provider', codes: [code('bash', `curl -u ${c.user}:${c.secret} \\\n  -X PUT \\\n  --data-binary @terraform-provider-myprovider_1.0.0_linux_amd64.zip \\\n  "${c.url}v1/providers/myorg/myprovider/1.0.0/upload/linux/amd64"`)] },
      { title: 'Publish a module', codes: [code('bash', `tar -czf vpc-1.0.0.tar.gz -C vpc/ .\ncurl -u ${c.user}:${c.secret} \\\n  -X PUT \\\n  --data-binary @vpc-1.0.0.tar.gz \\\n  "${c.url}v1/modules/myorg/vpc/aws/1.0.0"`)] },
    )
    return [tab('curl', 'curl', sections)]
  },

  huggingface: (c) => {
    const endpoint = c.url.replace(/\/$/, '')
    const cli: SetupSection[] = [
      { title: 'Point the client here', codes: [code('bash', `export HF_ENDPOINT=${endpoint}\nexport HF_TOKEN=${c.secret}\nhf download myorg/mymodel config.json`)] },
    ]
    if (c.hosted) cli.push({ title: 'Upload a file', text: 'One file per PUT, at the URL it is downloaded from. The Hub commit API is not implemented.', codes: [code('bash', `curl -u ${c.user}:${c.secret} \\\n  -X PUT \\\n  -T model.safetensors \\\n  "${c.url}myorg/mymodel/resolve/main/model.safetensors"`)] })
    const python: SetupSection[] = [{ title: 'huggingface_hub', codes: [code('python', `from huggingface_hub import HfApi\n\napi = HfApi(endpoint="${endpoint}")\npath = api.hf_hub_download("myorg/mymodel", "config.json")`)] }]
    return [tab('hf', 'hf CLI', cli), tab('python', 'Python', python)]
  },

  raw: rawClients,
}

/** An R list name: backquoted unless it is a syntactic identifier. */
function rIdent(name: string): string {
  return /^[A-Za-z][A-Za-z0-9._]*$/.test(name) ? name : `\`${name}\``
}

/** Formats with a Set Me Up builder — every format the server serves. */
export const SETUP_FORMATS = Object.keys(BUILDERS)
