<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { Bot, Radio, Shield, Save, CheckCircle2, AlertCircle, PhoneCall, KeyRound, Voicemail, Mic2, Square, RotateCcw, Upload, Smartphone, Download, Info, ShieldCheck, ChevronRight } from '@lucide/vue'
import PageHeader from '../components/PageHeader.vue'
import { state } from '../state'
import { api, del, post, put, uploadWav } from '../api'
import type { Settings } from '../types'
import { installPWA, pwa } from '../pwa'
const form=ref<Settings>({smsEnabled:true,callsEnabled:true,showTestTone:false,voicemailEnabled:false,voicemailTimeoutSeconds:30,telegramEnabled:false,telegramChatId:0,telegramToken:'',sipEnabled:false,providerUrl:'',providerApiKey:'',autoBlockCategories:['fraud','insurance'],country:'CN'})
const saved=ref(false),error=ref('')
const credential=ref<{username:string,password:string,server:string,transport:string}>()
const greeting=ref<{custom:boolean;durationSeconds:number}>({custom:false,durationSeconds:0})
const greetingBusy=ref(false),recordingGreeting=ref(false),greetingBlob=ref<Blob>(),greetingURL=ref('')
let greetingStream:MediaStream|undefined,greetingContext:AudioContext|undefined,greetingProcessor:ScriptProcessorNode|undefined
let greetingFrames:Float32Array[]=[],greetingRate=48000,greetingStarted=0,greetingLimit:number|undefined
watch(()=>state.snapshot?.settings,v=>{if(v)form.value={...v,telegramToken:'',providerApiKey:'',autoBlockCategories:[...v.autoBlockCategories]}},{immediate:true})
async function save(){saved.value=false;error.value='';try{await put('/api/v1/settings',form.value);saved.value=true;setTimeout(()=>saved.value=false,2500)}catch(e:any){error.value=e.code}}
function toggleCat(cat:string){const a=form.value.autoBlockCategories;a.includes(cat)?a.splice(a.indexOf(cat),1):a.push(cat)}
async function reveal(){error.value='';try{credential.value=await post('/api/v1/sip/credentials/reveal')}catch(e:any){error.value=e.code==='CREDENTIAL_ALREADY_REVEALED'?'凭据已经显示过；如已遗失，请重置密码。':e.code}}
async function resetCredential(){if(!confirm('重置后 Groundwire 必须使用新密码重新注册，继续？'))return;error.value='';try{credential.value=await post('/api/v1/sip/credentials/reset')}catch(e:any){error.value=e.code}}
function makeWav(frames:Float32Array[],sourceRate:number){
  const count=frames.reduce((n,f)=>n+f.length,0),source=new Float32Array(count)
  let at=0;for(const frame of frames){source.set(frame,at);at+=frame.length}
  const targetRate=16000,target=new Int16Array(Math.max(1,Math.floor(source.length*targetRate/sourceRate)))
  for(let i=0;i<target.length;i++){const p=i*sourceRate/targetRate,l=Math.floor(p),r=Math.min(source.length-1,l+1),v=source[l]*(1-(p-l))+source[r]*(p-l);target[i]=Math.max(-32768,Math.min(32767,Math.round(v*32767)))}
  const raw=new ArrayBuffer(44+target.byteLength),view=new DataView(raw),ascii=(o:number,s:string)=>{for(let i=0;i<s.length;i++)view.setUint8(o+i,s.charCodeAt(i))}
  ascii(0,'RIFF');view.setUint32(4,36+target.byteLength,true);ascii(8,'WAVE');ascii(12,'fmt ');view.setUint32(16,16,true);view.setUint16(20,1,true);view.setUint16(22,1,true);view.setUint32(24,targetRate,true);view.setUint32(28,targetRate*2,true);view.setUint16(32,2,true);view.setUint16(34,16,true);ascii(36,'data');view.setUint32(40,target.byteLength,true)
  new Int16Array(raw,44).set(target)
  return new Blob([raw],{type:'audio/wav'})
}
async function startGreeting(){
  error.value='';greetingFrames=[];greetingBlob.value=undefined
  try{
    greetingStream=await navigator.mediaDevices.getUserMedia({audio:{channelCount:1,echoCancellation:true,noiseSuppression:true}})
    greetingContext=new AudioContext();greetingRate=greetingContext.sampleRate
    const source=greetingContext.createMediaStreamSource(greetingStream)
    greetingProcessor=greetingContext.createScriptProcessor(4096,1,1)
    const mute=greetingContext.createGain();mute.gain.value=0
    greetingProcessor.onaudioprocess=e=>greetingFrames.push(new Float32Array(e.inputBuffer.getChannelData(0)))
    source.connect(greetingProcessor);greetingProcessor.connect(mute);mute.connect(greetingContext.destination)
    greetingStarted=Date.now();recordingGreeting.value=true
    greetingLimit=window.setTimeout(stopGreeting,30000)
  }catch(e:any){error.value=e.name==='NotAllowedError'?'麦克风权限被拒绝':(e.message||'无法录音')}
}
function stopGreeting(){
  if(!recordingGreeting.value)return
  recordingGreeting.value=false;if(greetingLimit)clearTimeout(greetingLimit)
  greetingProcessor?.disconnect();greetingProcessor=undefined;greetingStream?.getTracks().forEach(t=>t.stop());greetingStream=undefined
  void greetingContext?.close();greetingContext=undefined
  if(Date.now()-greetingStarted<300){error.value='提示语太短，请重新录制';return}
  greetingBlob.value=makeWav(greetingFrames,greetingRate)
  if(greetingURL.value)URL.revokeObjectURL(greetingURL.value)
  greetingURL.value=URL.createObjectURL(greetingBlob.value)
}
async function saveGreeting(){
  if(!greetingBlob.value)return
  greetingBusy.value=true;error.value=''
  try{greeting.value=await uploadWav('/api/v1/settings/voicemail-greeting',greetingBlob.value);greetingBlob.value=undefined;if(greetingURL.value)URL.revokeObjectURL(greetingURL.value);greetingURL.value=''}
  catch(e:any){error.value=e.code||e.message}finally{greetingBusy.value=false}
}
async function resetGreeting(){
  if(!confirm('恢复系统默认双语提示语？'))return
  greetingBusy.value=true
  try{await del('/api/v1/settings/voicemail-greeting');greeting.value={custom:false,durationSeconds:0}}
  catch(e:any){error.value=e.code||e.message}finally{greetingBusy.value=false}
}
onMounted(async()=>{try{greeting.value=await api('/api/v1/settings/voicemail-greeting')}catch{}})
onBeforeUnmount(()=>{if(recordingGreeting.value)stopGreeting();if(greetingURL.value)URL.revokeObjectURL(greetingURL.value)})
</script>
<template><section class="settings-page"><PageHeader eyebrow="系统" title="设置" description="设备功能、通知和应用选项。"><button class="primary compact settings-save" @click="save"><Save :size="17"/>保存更改</button></PageHeader><div v-if="saved" class="toast"><CheckCircle2/>设置已保存</div><div v-if="error" class="alert"><AlertCircle/><div><b>保存失败</b><p>{{error}}</p></div></div>
<div class="settings-grid"><article class="card settings-card mobile-app-card wide"><div class="setting-title"><span class="metric-icon green"><Smartphone/></span><div><h2>手机应用</h2><p>添加到主屏幕，像普通应用一样打开</p></div><span v-if="pwa.installed" class="tag active">已安装</span></div><div class="pwa-install-row"><button v-if="pwa.installable&&!pwa.installed" type="button" class="primary" :disabled="pwa.installing" @click="installPWA"><Download :size="17"/>{{pwa.installing?'正在打开安装…':'安装 onSIM'}}</button><p v-else-if="pwa.installed">onSIM 正以独立应用模式运行。</p><p v-else-if="pwa.ios">在 Safari 中点击“分享”，然后选择“添加到主屏幕”。</p><p v-else>打开浏览器菜单并选择“安装应用”或“添加到主屏幕”。</p><small v-if="pwa.message">{{pwa.message}}</small></div><nav class="mobile-settings-links"><RouterLink to="/voicemail"><Voicemail/><span><b>语音信箱</b><small>留言与提示语</small></span><ChevronRight/></RouterLink><RouterLink to="/info"><Info/><span><b>设备信息</b><small>手机、电话卡与网络</small></span><ChevronRight/></RouterLink><RouterLink to="/filters"><ShieldCheck/><span><b>防骚扰</b><small>黑名单与过滤规则</small></span><ChevronRight/></RouterLink></nav></article><article class="card settings-card"><div class="setting-title"><span class="metric-icon green"><Radio/></span><div><h2>电话与短信</h2><p>控制电话和短信功能</p></div></div><label class="switch-row"><div><b>电话功能</b><p>关闭后禁止拨出并自动拒接来电</p></div><input v-model="form.callsEnabled" type="checkbox"><span/></label><label class="switch-row"><div><b>短信功能</b><p>关闭后静默保存来信，不允许发送</p></div><input v-model="form.smsEnabled" type="checkbox"><span/></label><label class="switch-row"><div><b>显示通话测试音</b><p>仅排障时在通话面板显示测试音按钮</p></div><input v-model="form.showTestTone" type="checkbox"><span/></label></article>
<article class="card settings-card voicemail-settings"><div class="setting-title"><span class="metric-icon violet"><Voicemail/></span><div><h2>语音信箱</h2><p>无人接听时自动应答并保存留言</p></div></div><label class="switch-row"><div><b>启用语音信箱</b><p>超过设定响铃时间后自动接听</p></div><input v-model="form.voicemailEnabled" type="checkbox"><span/></label><label>自动接听等待时间（秒）<input v-model.number="form.voicemailTimeoutSeconds" type="number" min="10" max="120" step="5" :disabled="!form.voicemailEnabled"/></label>
<div class="greeting-recorder"><b>留言提示语</b><p>{{greeting.custom?`已使用自录提示语 · ${greeting.durationSeconds.toFixed(1)} 秒`:`当前使用系统默认双语提示语 · ${greeting.durationSeconds.toFixed(1)} 秒`}}</p><audio v-if="greetingBlob" controls :src="greetingURL"/><audio v-else controls preload="metadata" src="/api/v1/settings/voicemail-greeting/play"/>
<div class="greeting-actions"><button v-if="!recordingGreeting" type="button" class="secondary" @click="startGreeting"><Mic2 :size="16"/>{{greetingBlob?'重新录制':'录制提示语'}}</button><button v-else type="button" class="danger-button" @click="stopGreeting"><Square :size="16"/>停止录制</button><button v-if="greetingBlob" type="button" class="primary compact" :disabled="greetingBusy" @click="saveGreeting"><Upload :size="16"/>保存提示语</button><button v-if="greeting.custom" type="button" class="secondary" :disabled="greetingBusy" @click="resetGreeting"><RotateCcw :size="16"/>恢复默认</button></div><small v-if="recordingGreeting">正在录制，最长 30 秒…</small></div>
<p class="setting-help">提示语结束后会播放一声提示音，然后开始录制来电者的留言。</p></article>
<article class="card settings-card"><div class="setting-title"><span class="metric-icon amber"><Mic2/></span><div><h2>Safari 麦克风权限</h2><p>网页不会再在每次刷新时主动弹窗</p></div></div><p class="setting-help">若要永久允许：Safari → 此网站的设置 → 麦克风 → 允许。浏览器不允许网页代码自行授予永久权限；onSIM 会记住已经展示过权限请求，只在首次进入或你主动点击音频权限按钮时申请。</p></article>
<article class="card settings-card"><div class="setting-title"><span class="metric-icon blue"><Bot/></span><div><h2>Telegram Bot</h2><p>远程通知和确认操作</p></div></div><label class="switch-row"><div><b>启用 Telegram</b><p>使用长轮询，无需公网 Webhook</p></div><input v-model="form.telegramEnabled" type="checkbox"><span/></label><label>Bot Token<input v-model="form.telegramToken" type="password" placeholder="留空表示不修改"/></label><label>允许的 Chat ID<input v-model.number="form.telegramChatId" type="number" placeholder="123456789"/></label></article>
<article class="card settings-card wide"><div class="setting-title"><span class="metric-icon amber"><PhoneCall/></span><div><h2>SIP / Groundwire</h2><p>Asterisk 分机 1001 · {{state.snapshot?.device.sipStatus||'disabled'}}</p></div></div><label class="switch-row"><div><b>启用 SIP Gateway</b><p>与 Web 同时振铃；首个接听端独占音频</p></div><input v-model="form.sipEnabled" type="checkbox"><span/></label><div class="form-grid"><button type="button" class="secondary" @click="reveal"><KeyRound :size="17"/>首次查看凭据</button><button type="button" class="secondary" @click="resetCredential">重置分机密码</button></div><div v-if="credential" class="credential-box"><b>Groundwire 配置（密码离开本页后不再显示）</b><p>服务器：{{credential.server}} · 用户名：{{credential.username}} · 传输：{{credential.transport}}</p><code>{{credential.password}}</code></div><p v-if="state.snapshot?.device.sipPendingMessages">待投递短信：{{state.snapshot.device.sipPendingMessages}}</p></article>
<article class="card settings-card wide"><div class="setting-title"><span class="metric-icon violet"><Shield/></span><div><h2>号码识别 Provider</h2><p>仅允许 HTTPS；服务故障时不会自动拦截</p></div></div><div class="form-grid"><label>Provider URL<input v-model="form.providerUrl" type="url" placeholder="https://provider.example/lookup"/></label><label>API Key<input v-model="form.providerApiKey" type="password" placeholder="留空表示不修改"/></label></div><div class="category-picks"><b>特高风险自动拦截</b><button type="button" :class="{active:form.autoBlockCategories.includes('fraud')}" @click="toggleCat('fraud')">诈骗</button><button type="button" :class="{active:form.autoBlockCategories.includes('insurance')}" @click="toggleCat('insurance')">保险推销</button></div></article></div></section></template>
