import { describe, it, expect } from 'vitest'
import { readFileSync } from 'fs'
import { resolve } from 'path'
import {
  buildSetupGuide, envName, hostOf, hostnameOf, repoUrl, trimBase,
  SECRET_PLACEHOLDER, SETUP_FORMATS, USERNAME_PLACEHOLDER, type SetupGuide,
} from './snippets'

const BASE = 'https://nx.example.com:8443'

function all(g: SetupGuide): string {
  return g.clients.flatMap(c => c.sections.flatMap(s => [s.title, s.text ?? '', s.note ?? '', ...s.codes.map(x => `${x.label ?? ''}\n${x.content}`)])).join('\n')
}

function guide(format: string, repoType: string, repoName = `${format}-${repoType}`, username?: string) {
  return buildSetupGuide({ format, repoName, repoType, baseUrl: `${BASE}/`, username })
}

/** What only a hosted repository gets: the publish / upload step of each client. */
const PUSH: Record<string, RegExp> = {
  maven2: /distributionManagement|deploy:deploy-file|gradlew publish/,
  npm: /npm publish|pnpm publish|yarn npm publish/,
  pypi: /twine upload|uv publish|poetry publish/,
  docker: /docker push/,
  oci: /helm push|oras push|docker push/,
  go: /-T v1\.0\.0\.zip/,
  nuget: /nuget push/,
  helm: /-T mychart-1\.0\.0\.tgz/,
  cargo: /cargo publish/,
  rubygems: /gem push/,
  conan: /conan upload/,
  apt: /-T mypackage_1\.0_amd64\.deb/,
  yum: /-T mypackage-1\.0-1\.x86_64\.rpm/,
  alpine: /-T mypackage-1\.0\.0-r0\.apk/,
  cran: /-T mypackage_1\.0\.0\.tar\.gz/,
  conda: /-T mypackage-1\.0\.0-py311_0\.conda/,
  terraform: /upload\/linux\/amd64|-X PUT/,
  huggingface: /-X PUT/,
  raw: /-T myfile\.tar\.gz/,
}

describe('url helpers', () => {
  it('normalises the base URL', () => {
    expect(trimBase('http://h:1//')).toBe('http://h:1')
    expect(hostOf('https://h.example:8443/')).toBe('h.example:8443')
    expect(hostnameOf('https://h.example:8443')).toBe('h.example')
    expect(repoUrl('http://h/', 'r')).toBe('http://h/repository/r/')
    expect(envName('pypi-hosted.v2')).toBe('PYPI_HOSTED_V2')
  })
})

describe('buildSetupGuide', () => {
  it('covers every format the server serves', () => {
    // domain.AllFormats is the server's list; keep the dialog in step with it.
    const src = readFileSync(resolve(__dirname, '../../../internal/domain/types.go'), 'utf8')
    const block = src.slice(src.indexOf('var AllFormats'), src.indexOf('}', src.indexOf('var AllFormats')))
    const consts = [...block.matchAll(/Format(\w+),/g)].map(m => m[1])
    const values = consts.map(name => new RegExp(`Format${name}\\s+RepoFormat = "([^"]+)"`).exec(src)![1])
    expect(values.length).toBeGreaterThan(15)
    expect([...SETUP_FORMATS].sort()).toEqual([...values].sort())
  })

  for (const format of Object.keys(PUSH)) {
    describe(format, () => {
      it('fills in the repository URL and offers at least one client', () => {
        const g = guide(format, 'hosted')
        expect(g.repoUrl).toBe(`${BASE}/repository/${format}-hosted/`)
        expect(g.clients.length).toBeGreaterThan(0)
        for (const c of g.clients) expect(c.sections.length).toBeGreaterThan(0)
        const text = all(g)
        expect(text).toContain(format === 'docker' || format === 'oci' ? `nx.example.com:8443/${format}-hosted/` : `${BASE}/repository/${format}-hosted`)
        expect(g.note).toBeUndefined()
      })

      it('includes publish instructions only for hosted', () => {
        expect(all(guide(format, 'hosted'))).toMatch(PUSH[format])
        for (const type of ['proxy', 'group']) {
          const g = guide(format, type)
          expect(all(g), `${format} ${type}`).not.toMatch(PUSH[format])
          expect(g.note).toMatch(/hosted repository/)
        }
      })

      it('substitutes the username and never renders a secret', () => {
        const withUser = all(guide(format, 'hosted', 'r', 'alice'))
        expect(withUser).not.toContain(USERNAME_PLACEHOLDER)
        expect(withUser).not.toMatch(/admin123|nxs_/)
        const anon = all(guide(format, 'hosted', 'r'))
        expect(anon).not.toContain('alice')
        // Wherever a credential is printed, it is the placeholder.
        if (/-u |password|Password|_authToken|AuthToken|TOKEN/.test(anon)) expect(anon).toContain(SECRET_PLACEHOLDER)
      })
    })
  }

  it('falls back to generic curl for an unknown format', () => {
    const g = guide('mystery', 'hosted', 'm')
    expect(g.clients.map(c => c.id)).toEqual(['curl'])
    expect(all(g)).toContain(`${BASE}/repository/m/path/to/myfile.tar.gz`)
  })

  it('treats the format case-insensitively', () => {
    expect(guide('NPM', 'hosted', 'n').clients.map(c => c.id)).toEqual(['npm', 'pnpm', 'yarn'])
  })
})

describe('client tabs and endpoint shapes', () => {
  const tabs = (format: string) => guide(format, 'hosted').clients.map(c => c.label)

  it('offers the usual clients for the high-config formats', () => {
    expect(tabs('maven2')).toEqual(['Maven', 'Gradle'])
    expect(tabs('npm')).toEqual(['npm', 'pnpm', 'yarn'])
    expect(tabs('pypi')).toEqual(['pip', 'uv', 'poetry'])
    expect(tabs('docker')).toEqual(['docker', 'Compose'])
    expect(tabs('oci')).toEqual(['helm', 'oras', 'docker'])
    expect(tabs('nuget')).toEqual(['dotnet', 'nuget.config'])
    expect(tabs('helm')).toEqual(['helm'])
  })

  it('maven: a proxy or group is a mirror, a hosted repo is a deploy target', () => {
    const hosted = all(guide('maven2', 'hosted', 'libs', 'bob'))
    expect(hosted).toContain('<url>https://nx.example.com:8443/repository/libs/</url>')
    expect(hosted).toContain('<username>bob</username>')
    expect(hosted).toContain(`<password>${SECRET_PLACEHOLDER}</password>`)
    expect(all(guide('maven2', 'group'))).toContain('<mirrorOf>*</mirrorOf>')
  })

  it('npm: token keyed to the registry path, scoped variant, no npm login', () => {
    const text = all(guide('npm', 'hosted', 'npm-hosted'))
    expect(text).toContain(`registry=${BASE}/repository/npm-hosted/\n//nx.example.com:8443/repository/npm-hosted/:_authToken=${SECRET_PLACEHOLDER}`)
    expect(text).toContain(`@myscope:registry=${BASE}/repository/npm-hosted/`)
    expect(text).not.toMatch(/^npm (login|adduser)(\s+--|\s*$)/m)
  })

  it('pypi: simple index for reads, repository root for uploads', () => {
    const text = all(guide('pypi', 'hosted', 'py', 'carol'))
    expect(text).toContain(`index-url = ${BASE}/repository/py/simple/`)
    expect(text).toContain('trusted-host = nx.example.com\n')
    expect(text).toContain(`--repository-url ${BASE}/repository/py/ \\`)
    expect(text).toContain('UV_INDEX_PY_USERNAME=carol')
  })

  it('docker: repository name is the first path segment', () => {
    const text = all(guide('docker', 'hosted', 'dh', 'dan'))
    expect(text).toContain(`docker login nx.example.com:8443 -u dan -p ${SECRET_PLACEHOLDER}`)
    expect(text).toContain('docker push nx.example.com:8443/dh/myapp:1.0')
    expect(text).toContain(`"${BASE}/v2/dh/myapp/tags/list"`)
    expect(all(guide('docker', 'proxy', 'hub'))).toContain('docker pull nx.example.com:8443/hub/library/nginx:latest')
  })

  it('nuget: v3 index.json source, push by source name', () => {
    const text = all(guide('nuget', 'hosted', 'ng'))
    expect(text).toContain(`dotnet nuget add source ${BASE}/repository/ng/index.json`)
    expect(text).toContain('--source ng')
  })

  it('cargo: sparse index under index/, Bearer token', () => {
    const text = all(guide('cargo', 'hosted', 'crates'))
    expect(text).toContain(`index = "sparse+${BASE}/repository/crates/index/"`)
    expect(text).toContain(`cargo login --registry crates "Bearer ${SECRET_PLACEHOLDER}"`)
  })

  it('go, apt, yum, cran, terraform, huggingface point at the repository', () => {
    expect(all(guide('go', 'proxy', 'gp'))).toContain(`GOPROXY="${BASE}/repository/gp/|direct"`)
    expect(all(guide('apt', 'hosted', 'deb'))).toContain(`deb [trusted=yes] ${BASE}/repository/deb/ stable main`)
    expect(all(guide('yum', 'hosted', 'rpm'))).toContain(`baseurl=${BASE}/repository/rpm/`)
    expect(all(guide('cran', 'hosted', 'cran-hosted'))).toContain('options(repos = c(`cran-hosted` = ')
    expect(all(guide('cran', 'hosted', 'cran'))).toContain('options(repos = c(cran = ')
    expect(all(guide('terraform', 'hosted', 'tf'))).toContain(`${BASE}/repository/tf/.well-known/terraform.json`)
    expect(all(guide('huggingface', 'proxy', 'hf'))).toContain(`HF_ENDPOINT=${BASE}/repository/hf\n`)
  })
})
