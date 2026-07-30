<script setup lang="ts">
import { computed, ref } from 'vue'
import { Download, PhoneIncoming, Voicemail, Trash2 } from '@lucide/vue'
import PageHeader from '../components/PageHeader.vue'
import { state } from '../state'
import { del } from '../api'

const messages=computed(()=>state.snapshot?.recordings.filter(recording=>recording.kind==='voicemail')||[])
const deleting=ref('')
function callFor(callId:string){return state.snapshot?.calls.find(call=>call.id===callId)}
const time=(value:string)=>new Intl.DateTimeFormat('zh-CN',{dateStyle:'medium',timeStyle:'short'}).format(new Date(value))
async function remove(id:string){
  if(!confirm('永久删除这条语音留言？删除后无法恢复。'))return
  deleting.value=id
  try{await del(`/api/v1/recordings/${id}`)}
  finally{deleting.value=''}
}
</script>

<template>
  <section>
    <PageHeader eyebrow="电话" title="语音信箱" description="无人接听时由 onSIM 自动保存的来电留言。"/>
    <article class="card table-card voicemail-list">
      <div v-for="message in messages" :key="message.id" class="table-row">
        <span class="metric-icon violet"><Voicemail/></span>
        <div class="grow">
          <b>{{callFor(message.callId)?.displayName||callFor(message.callId)?.number||'未知来电'}}</b>
          <p><PhoneIncoming :size="13"/>{{message.durationSeconds}} 秒 · {{Math.ceil(message.size/1024)}} KB</p>
        </div>
        <time>{{time(message.createdAt)}}</time>
        <audio controls preload="metadata" playsinline :src="`/api/v1/recordings/${message.id}/play`"/>
        <a class="icon-btn" :href="`/api/v1/recordings/${message.id}`" download title="下载留言"><Download/></a>
        <button class="icon-btn danger" :disabled="deleting===message.id" title="永久删除留言" @click="remove(message.id)"><Trash2/></button>
      </div>
      <div v-if="!messages.length" class="voicemail-empty"><Voicemail/><h3>暂无语音留言</h3><p>开启语音信箱后，无人接听的来电会显示在这里。</p></div>
    </article>
  </section>
</template>
