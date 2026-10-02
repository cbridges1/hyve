import logoDark from '../assets/logo-dark.svg'
import logoLight from '../assets/logo-light.svg'

/** Renders the light-theme wordmark by default, swapping to the dark-theme one via Tailwind's dark: variant — no JS/theme-detection needed, matches how every other dark: class in this app already tracks prefers-color-scheme. */
export function Logo({ className = 'h-5' }: { className?: string }) {
  return (
    <>
      <img src={logoLight} alt="hyve" className={`${className} dark:hidden`} />
      <img src={logoDark} alt="hyve" className={`hidden ${className} dark:block`} />
    </>
  )
}
