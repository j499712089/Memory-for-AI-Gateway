import { readdir, readFile } from 'node:fs/promises'
import { extname, join, relative, resolve } from 'node:path'

const sourceRoot = resolve(process.cwd(), 'src')
const sourceExtensions = new Set(['.css', '.html', '.js', '.jsx', '.mjs', '.scss', '.svelte', '.ts', '.tsx', '.vue'])
const emojiPattern = /[\u{1F000}-\u{1FAFF}\u{2600}-\u{27BF}]/gu

async function collectSourceFiles(directory) {
  const entries = await readdir(directory, { withFileTypes: true })
  const files = await Promise.all(
    entries.map(async (entry) => {
      const entryPath = join(directory, entry.name)
      if (entry.isDirectory()) return collectSourceFiles(entryPath)
      return sourceExtensions.has(extname(entry.name)) ? [entryPath] : []
    }),
  )
  return files.flat()
}

const violations = []
for (const filePath of await collectSourceFiles(sourceRoot)) {
  const lines = (await readFile(filePath, 'utf8')).split(/\r?\n/u)
  lines.forEach((line, lineIndex) => {
    for (const match of line.matchAll(emojiPattern)) {
      violations.push({
        column: match.index + 1,
        file: relative(process.cwd(), filePath),
        line: lineIndex + 1,
        symbol: match[0],
      })
    }
  })
}

if (violations.length > 0) {
  console.error('Emoji source gate failed:')
  for (const violation of violations) {
    console.error(`${violation.file}:${violation.line}:${violation.column} ${violation.symbol}`)
  }
  process.exitCode = 1
} else {
  console.log('Emoji source gate passed.')
}
