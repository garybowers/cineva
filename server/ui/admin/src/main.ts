import { mount } from 'svelte'
import '@fontsource-variable/inter'
import './app.css'
import { applyTheme, currentTheme } from './lib/theme'
import App from './App.svelte'
import TvNotice from './TvNotice.svelte'

applyTheme(currentTheme())

// On a screen set up as a TV (the client's interface preference, kept on
// this browser, or a television's own browser on Automatic) the admin
// pages are not offered: see docs/tv.md.
// The same list as the client's lib/tv.svelte.ts.
const TV_BROWSER = /AFT[A-Z]|Android TV|GoogleTV|BRAVIA|Web0S|webOS|Tizen|SMART-TV|SmartTV|HbbTV|CrKey|AppleTV/i
function onTV(): boolean {
  let setting = 'auto'
  try {
    setting = localStorage.getItem('cineva-interface') ?? 'auto'
  } catch {
    /* private mode: go by the browser */
  }
  return setting === 'tv' || (setting !== 'desktop' && TV_BROWSER.test(navigator.userAgent))
}

const app = mount(onTV() ? TvNotice : App, { target: document.getElementById('app')! })

export default app
