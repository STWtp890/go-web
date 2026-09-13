import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

const sourceFiles = [
  '../src/router/index.ts',
  '../src/layouts/AppLayout.vue',
  '../src/views/app/OverviewView.vue',
]

const forbiddenPatterns = [
  { pattern: /@\/api\/chat/, description: 'Chat API import' },
  { pattern: /name:\s*['"]chat['"]/, description: 'Chat named route' },
  { pattern: /\/api\/v1\/protected\/chat(?:\/|['"`])/, description: 'Chat HTTP endpoint' },
  { pattern: /ChatView\.vue/, description: 'Chat view registration' },
]

const violations = []

for (const relativePath of sourceFiles) {
  const absolutePath = fileURLToPath(new URL(relativePath, import.meta.url))
  const source = readFileSync(absolutePath, 'utf8')

  for (const { pattern, description } of forbiddenPatterns) {
    if (pattern.test(source)) violations.push(`${relativePath}: ${description}`)
  }
}

if (violations.length > 0) {
  console.error('Phase boundary check failed:')
  for (const violation of violations) console.error(`- ${violation}`)
  process.exit(1)
}

console.log('PHASE_BOUNDARY=PASS')
