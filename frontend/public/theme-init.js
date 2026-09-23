// Applies the saved color theme before the first paint, so a light-theme user
// does not see a flash of the dark UI while the bundle loads. It is a file, not
// an inline <script>: the server's CSP is script-src 'self'.
// Keep the key and values in sync with src/theme/theme.ts.
(function () {
  var theme = 'dark'
  try {
    if (window.localStorage.getItem('nexspence-theme') === 'light') theme = 'light'
  } catch (e) {
    // Storage unavailable — keep the default.
  }
  document.documentElement.setAttribute('data-theme', theme)
})()
