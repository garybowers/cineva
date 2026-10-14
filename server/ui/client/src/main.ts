import { mount } from 'svelte'
import '@fontsource-variable/inter'
import './app.css'
import { applyTheme, currentTheme } from './lib/theme'
import { applyInterface, installSpatial } from './lib/tv.svelte'
import App from './App.svelte'

applyTheme(currentTheme())
// TV mode, from this device's preference (its local copy, so it is right
// before sign-in) or from what browser this is.
applyInterface()
installSpatial()

const app = mount(App, { target: document.getElementById('app')! })

// Register the service worker after the app is up, so it never competes
// with the first paint. Only in a built app: under `vite dev` the worker
// would cache the dev server's modules and serve them stale after an edit.
// A failure here costs installability, not the app, so it is logged and
// swallowed rather than thrown.
if ('serviceWorker' in navigator && import.meta.env.PROD) {
  window.addEventListener('load', () => {
    navigator.serviceWorker.register('/sw.js').catch((err) => {
      console.warn('service worker registration failed', err)
    })
  })
}

export default app
