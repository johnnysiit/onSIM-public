<script setup lang="ts">
import { ref } from 'vue'
import { Delete, Phone } from '@lucide/vue'
import PageHeader from '../components/PageHeader.vue'
import { post } from '../api'
import { playDTMFKey } from '../callAudio'
import GatewayRouteSelect from '../components/GatewayRouteSelect.vue'
import { selectedGatewayRoute } from '../gateways'
const number=ref(''),error=ref(''),busy=ref(false)
const keys=[['1',''],['2','ABC'],['3','DEF'],['4','GHI'],['5','JKL'],['6','MNO'],['7','PQRS'],['8','TUV'],['9','WXYZ'],['*',''],['0','+'],['#','']]
function press(k:string,sub:string){playDTMFKey(k);if(k==='0'&&sub==='+'&&number.value==='')number.value+='+';else number.value+=k}
async function dial(){error.value='';busy.value=true;try{const route=selectedGatewayRoute.value;await post('/api/v1/calls',{number:number.value,gatewayId:route?.gatewayId,subscriptionId:route?.subscriptionId})}catch(e:any){error.value=e.code}finally{busy.value=false}}
</script>
<template><section><PageHeader eyebrow="电话" title="拨号盘" description="输入号码，并选择本次通话使用的电话卡。"/><article class="dialer card"><GatewayRouteSelect/><div class="number-input"><input v-model="number" inputmode="tel" placeholder="输入电话号码"/><button @click="number=number.slice(0,-1)"><Delete/></button></div><div class="keypad"><button v-for="[k,sub] in keys" :key="k" @click="press(k,sub)"><b>{{k}}</b><small>{{sub}}</small></button></div><p v-if="error" class="form-error">{{error}}</p><button class="call-button" :disabled="!number||busy||!selectedGatewayRoute?.connected" @click="dial"><Phone fill="currentColor"/><span>{{busy?'正在呼叫':'拨打'}}</span></button></article></section></template>
