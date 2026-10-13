// Builds the API reference page into dist/, which the Go server embeds and
// serves at /api/docs. Swagger UI comes pre-built from swagger-ui-dist, so
// this only copies it beside our page; there is nothing to compile.
import { cpSync, mkdirSync, rmSync } from 'node:fs'

const vendor = 'node_modules/swagger-ui-dist'
rmSync('dist', { recursive: true, force: true })
mkdirSync('dist')
for (const f of ['swagger-ui-bundle.js', 'swagger-ui.css']) cpSync(`${vendor}/${f}`, `dist/${f}`)
// Apache-2.0 asks for its licence and notices to travel with the code.
cpSync(`${vendor}/LICENSE`, 'dist/swagger-ui-LICENSE.txt')
cpSync(`${vendor}/NOTICE`, 'dist/swagger-ui-NOTICE.txt')
cpSync('src', 'dist', { recursive: true })
cpSync('../client/public/favicon.svg', 'dist/favicon.svg')
