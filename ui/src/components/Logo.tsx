import logoDark from '../assets/logo-dark.svg'
import logoLight from '../assets/logo-light.svg'

export function Logo({ className = 'h-5' }: { className?: string }) {
  return (
    <>
      <img src={logoLight} alt="hyve" className={`${className} dark:hidden`} />
      <img src={logoDark} alt="hyve" className={`hidden ${className} dark:block`} />
    </>
  )
}
