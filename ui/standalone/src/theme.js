// Light, dark, or the system's: the console's three-way theme, remembered
// in the browser.
import { ref } from 'vue';

const KEY = 'hyve-theme';
const media = window.matchMedia('(prefers-color-scheme: dark)');

function stored() {
  try {
    const v = window.localStorage.getItem(KEY);

    return v === 'light' || v === 'dark' || v === 'system' ? v : 'system';
  } catch (e) {
    return 'system';
  }
}

export const theme = ref(stored());

function apply() {
  const dark = theme.value === 'dark' || (theme.value === 'system' && media.matches);

  document.documentElement.classList.toggle('dark', dark);
  document.documentElement.style.colorScheme = dark ? 'dark' : 'light';
}

export function setTheme(v) {
  theme.value = v;
  try {
    window.localStorage.setItem(KEY, v);
  } catch (e) {}
  apply();
}

media.addEventListener('change', () => theme.value === 'system' && apply());
apply();
