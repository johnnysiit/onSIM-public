import { reactive } from 'vue'

interface InstallPromptEvent extends Event {
  prompt: () => Promise<void>
  userChoice: Promise<{ outcome: 'accepted' | 'dismissed'; platform: string }>
}

const standalone = () =>
  window.matchMedia('(display-mode: standalone)').matches ||
  Boolean((navigator as Navigator & { standalone?: boolean }).standalone)

export const pwa = reactive({
  installed: standalone(),
  installable: false,
  installing: false,
  ios: /iphone|ipad|ipod/i.test(navigator.userAgent),
  message: '',
})

let installPrompt: InstallPromptEvent | undefined
let initialized = false

export function initPWA() {
  if (initialized) return
  initialized = true
  window.addEventListener('beforeinstallprompt', event => {
    event.preventDefault()
    installPrompt = event as InstallPromptEvent
    pwa.installable = true
  })
  window.addEventListener('appinstalled', () => {
    installPrompt = undefined
    pwa.installable = false
    pwa.installed = true
    pwa.message = 'onSIM 已添加到主屏幕'
  })
  window.matchMedia('(display-mode: standalone)').addEventListener('change', () => {
    pwa.installed = standalone()
  })
}

export async function installPWA() {
  if (!installPrompt || pwa.installing) return
  pwa.installing = true
  pwa.message = ''
  try {
    await installPrompt.prompt()
    const choice = await installPrompt.userChoice
    if (choice.outcome === 'accepted') {
      installPrompt = undefined
      pwa.installable = false
    } else {
      pwa.message = '已取消安装，可稍后再试'
    }
  } finally {
    pwa.installing = false
  }
}
