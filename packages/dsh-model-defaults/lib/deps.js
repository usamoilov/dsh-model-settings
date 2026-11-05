/**
 * Libraries borrowed from the installed DSH: the `yaml` document editor that
 * keeps comments (the one DSH's own config editor uses) and DSH's profile file
 * lock + atomic write. The plugin ships no dependencies of its own, so they are
 * resolved from the DSH installation that runs it.
 */
import { createRequire } from 'node:module'
import { existsSync, realpathSync } from 'node:fs'
import path from 'node:path'
import { pathToFileURL } from 'node:url'

let dshRoot = null
let yamlLib = null
let atomicLib = null

/** The `dsh` command on PATH, if any. */
function dshOnPath() {
  for (const dir of String(process.env.PATH || '').split(path.delimiter)) {
    const file = dir && path.join(dir, 'dsh')
    if (file && existsSync(file)) return file
  }
  return null
}

/**
 * Root folder of the installed `@deepseek-ai/dsh` package: `DSH_ROOT` if set,
 * else the package of the running DSH (`process.argv[1]` is its `lib/bin.js`,
 * possibly through a symlink), else the package of `dsh` on PATH.
 */
export function findDshRoot() {
  if (dshRoot) return dshRoot
  const candidates = process.env.DSH_ROOT ? [process.env.DSH_ROOT] : []
  for (const bin of [process.argv[1], dshOnPath()]) {
    try {
      if (bin) candidates.push(path.dirname(path.dirname(realpathSync(bin))))
    } catch {
      // not a file
    }
  }
  for (const dir of candidates) {
    if (existsSync(path.join(dir, 'node_modules', 'yaml', 'package.json'))) {
      dshRoot = dir
      return dir
    }
  }
  throw new Error('DSH installation not found: run inside DSH, put `dsh` on PATH, or set DSH_ROOT to the @deepseek-ai/dsh package folder')
}

/** The `yaml` package (Document API that preserves comments). */
export function loadYaml() {
  if (!yamlLib) yamlLib = createRequire(path.join(findDshRoot(), 'package.json'))('yaml')
  return yamlLib
}

/** `withFileLock` and `writeFileAtomic` from `@deepseek-ai/dsh-atomic-write`. */
export async function loadAtomicWrite() {
  if (!atomicLib) {
    const file = path.join(findDshRoot(), 'node_modules', '@deepseek-ai', 'dsh-atomic-write', 'lib', 'index.js')
    atomicLib = await import(pathToFileURL(file).href)
  }
  return atomicLib
}

/** DSH's entry script, for `--dump-config` checks run with the current Node. */
export function dshEntry() {
  return path.join(findDshRoot(), 'lib', 'bin.js')
}
