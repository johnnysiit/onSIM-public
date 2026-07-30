import { createApp } from 'vue'
import { createRouter, createWebHistory } from 'vue-router'
import App from './App.vue'
import AuthView from './views/AuthView.vue'
import DashboardView from './views/DashboardView.vue'
import DialerView from './views/DialerView.vue'
import CallsView from './views/CallsView.vue'
import MessagesView from './views/MessagesView.vue'
import FiltersView from './views/FiltersView.vue'
import SettingsView from './views/SettingsView.vue'
import InfoView from './views/InfoView.vue'
import TempCallView from './views/TempCallView.vue'
import VoicemailView from './views/VoicemailView.vue'
import './style.css'

const router=createRouter({history:createWebHistory(),routes:[
  {path:'/auth',component:AuthView,meta:{public:true}},
  {path:'/temp-call',component:TempCallView,meta:{public:true,temp:true}},
  {path:'/',component:DashboardView},
  {path:'/dial',component:DialerView},
  {path:'/calls',component:CallsView},
  {path:'/messages',component:MessagesView},
  {path:'/voicemail',component:VoicemailView},
  {path:'/info',component:InfoView},
  {path:'/filters',component:FiltersView},
  {path:'/settings',component:SettingsView},
]})

router.beforeEach(async to=>{
  if(to.meta.public)return true
  const res=await fetch('/api/v1/auth/status').then(r=>r.json()).catch(()=>({authenticated:false}))
  if(!res.authenticated)return '/auth'
  return true
})

createApp(App).use(router).mount('#app')

if ('serviceWorker' in navigator && import.meta.env.PROD) navigator.serviceWorker.register('/sw.js')
