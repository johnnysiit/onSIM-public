<script setup lang="ts">
import { computed } from 'vue'
const props=withDefaults(defineProps<{value?:number;registered?:boolean;compact?:boolean}>(),{value:-1,registered:false,compact:false})
const level=computed(()=>{
  if(!props.registered||props.value<0||props.value===99)return 0
  if(props.value<=7)return 1
  if(props.value<=14)return 2
  if(props.value<=21)return 3
  return 4
})
const description=computed(()=>['无信号','较弱','一般','良好','优秀'][level.value])
</script>
<template><span class="signal-bars" :class="{compact}" role="img" :aria-label="`蜂窝信号：${description}`" :title="`蜂窝信号：${description}`"><i v-for="bar in 4" :key="bar" :class="{active:bar<=level}"/></span></template>
