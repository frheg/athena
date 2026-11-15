import { mount } from 'svelte'
import '@fontsource-variable/geist'
import '@fontsource-variable/geist-mono'
import '@fontsource/instrument-serif'
import './app.css'
import App from './App.svelte'

mount(App, { target: document.getElementById('app') })
