import { describe, it, expect } from 'vitest'
import { repositoryPath } from './repositoryPath'

describe('repositoryPath', () => {
  it('joins the repository and the asset path', () => {
    expect(repositoryPath('maven-hosted', '/com/acme/app/1.0/app-1.0.jar'))
      .toBe('/repository/maven-hosted/com/acme/app/1.0/app-1.0.jar')
  })

  // #570: "demo#2" unencoded sends the request to repository "demo".
  it('encodes the repository name as one segment', () => {
    expect(repositoryPath('demo#2', 'a.txt')).toBe('/repository/demo%232/a.txt')
    expect(repositoryPath('a?b%c', 'a.txt')).toBe('/repository/a%3Fb%25c/a.txt')
  })

  it('escapes only ? and # in the asset path, keeping its slashes', () => {
    expect(repositoryPath('raw', 'dir/x#1?.txt')).toBe('/repository/raw/dir/x%231%3F.txt')
    expect(repositoryPath('raw', 'dir/a b+c.txt')).toBe('/repository/raw/dir/a b+c.txt')
  })
})
