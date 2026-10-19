import { reactive } from 'vue';

// Who's signed in, and the last error a page's fetch hit.
export const session = reactive({ user: null, version: '', error: '' });
