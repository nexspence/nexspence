/// <reference types="vite/client" />

declare module '*.module.css' {
  const classes: Record<string, string>
  export default classes
}

declare module '*.png' {
  const src: string
  export default src
}
