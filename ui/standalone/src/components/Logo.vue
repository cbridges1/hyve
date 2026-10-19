<script setup>
import light from '../assets/logo-light.svg';
import dark from '../assets/logo-dark.svg';

defineProps({ size: { type: String, default: 'h-5' } });
</script>

<template>
  <img
    :src="light"
    alt="hyve"
    :class="[size, 'dark:hidden']"
  >
  <img
    :src="dark"
    alt="hyve"
    :class="['hidden', size, 'dark:block']"
  >
</template>
