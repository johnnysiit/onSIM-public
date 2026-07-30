<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { LockKeyhole, Radio } from '@lucide/vue'
import { post } from '../api'
const router=useRouter(),password=ref(''),confirm=ref(''),initialized=ref(true),error=ref(''),busy=ref(false)
onMounted(async()=>{const s=await fetch('/api/v1/auth/status').then(r=>r.json());initialized.value=s.initialized;if(s.authenticated)router.replace('/')})
async function submit(){error.value='';if(!initialized.value&&password.value!==confirm.value){error.value='两次密码不一致';return};busy.value=true;try{await post(initialized.value?'/api/v1/auth/login':'/api/v1/auth/setup',{password:password.value});router.replace('/')}catch(e:any){error.value=e.code==='PASSWORD_TOO_SHORT'?'密码至少需要 10 个字符':'密码不正确或请求过于频繁'}finally{busy.value=false}}
</script>
<template><main class="auth-page"><section class="auth-card"><div class="auth-logo"><Radio/><span>onSIM</span></div><div class="auth-icon"><LockKeyhole/></div><p class="eyebrow">{{initialized?'安全访问':'首次启动'}}</p><h1>{{initialized?'欢迎回来':'创建管理员密码'}}</h1><p>{{initialized?'输入密码以进入电话控制台。':'该密码将保护短信、通话记录和设备设置。'}}</p><form @submit.prevent="submit"><label>密码<input v-model="password" type="password" minlength="10" required autofocus placeholder="••••••••••"/></label><label v-if="!initialized">确认密码<input v-model="confirm" type="password" minlength="10" required placeholder="再次输入密码"/></label><p v-if="error" class="form-error">{{error}}</p><button class="primary" :disabled="busy">{{busy?'正在验证…':initialized?'登录':'完成设置'}}</button></form><small>仅限局域网 / VPN · Session 安全保护</small></section></main></template>
