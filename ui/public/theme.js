// Applies the remembered theme before the app loads (src/lib/theme.ts keeps
// it in sync afterwards).
(function () {
  var stored = null
  try {
    stored = localStorage.getItem('hyve-theme')
  } catch (_) {}
  var theme = stored === 'light' || stored === 'dark' ? stored : 'system'
  var dark = theme === 'dark' || (theme === 'system' && window.matchMedia('(prefers-color-scheme: dark)').matches)
  document.documentElement.classList.toggle('dark', dark)
  document.documentElement.style.colorScheme = dark ? 'dark' : 'light'
})()
