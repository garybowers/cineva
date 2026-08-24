import { mount } from 'svelte'
import './app.css'
import { applyTheme, currentTheme } from './lib/theme'
import App from './App.svelte'

applyTheme(currentTheme())

const app = mount(App, { target: document.getElementById('app')! })

export default app
