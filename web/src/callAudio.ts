import { reactive } from 'vue'

export const callAudioState=reactive({
  microphone:'unknown' as 'unknown'|'requesting'|'granted'|'denied'|'unavailable',
  playback:'locked' as 'locked'|'ready'|'failed',
  error:''
})

let context:AudioContext|undefined
let requested=false
let unlockListenersInstalled=false
const permissionPromptKey='onsim.microphonePermissionPrompted'

function audioContext(){
  if(!context||context.state==='closed')context=new AudioContext()
  return context
}

async function unlockPlayback(){
  try{
    const ctx=audioContext()
    await ctx.resume()
    callAudioState.playback=ctx.state==='running'?'ready':'locked'
    if(ctx.state==='running'&&callAudioState.error==='请点击页面以启用提醒声音')callAudioState.error=''
    return ctx.state==='running'
  }catch(e:any){
    callAudioState.playback='failed'
    callAudioState.error=e?.message||'AUDIO_PLAYBACK_UNAVAILABLE'
    return false
  }
}

/**
 * Browsers only permit audible playback after a user gesture. Installing the
 * capture listener before login means the first click/tap anywhere in onSIM
 * unlocks later call and SMS alerts without requiring a second special click.
 */
export function installAudioUnlock(){
  if(unlockListenersInstalled)return
  unlockListenersInstalled=true
  const unlock=()=>{
    void unlockPlayback().then(ready=>{
      if(!ready)return
      window.removeEventListener('pointerdown',unlock,true)
      window.removeEventListener('keydown',unlock,true)
    })
  }
  window.addEventListener('pointerdown',unlock,true)
  window.addEventListener('keydown',unlock,true)
}

/** Request microphone access as soon as the authenticated console opens. */
export async function requestCallAudioAccess(force=false){
  if(requested)return
  if(!force&&localStorage.getItem(permissionPromptKey)==='yes'){
    try{
      const permission=await navigator.permissions?.query({name:'microphone' as PermissionName})
      if(permission?.state!=='granted'){
        callAudioState.error='麦克风未自动请求；通话时点击权限提示，或在 Safari 网站设置中设为“允许”'
        return
      }
    }catch{
      callAudioState.error='已记住权限引导；需要时请点击页面上的音频权限按钮'
      return
    }
  }
  requested=true
  if(!navigator.mediaDevices?.getUserMedia){
    callAudioState.microphone='unavailable'
    callAudioState.error='浏览器不支持麦克风，或当前页面不是安全连接'
    return
  }
  callAudioState.microphone='requesting'
  localStorage.setItem(permissionPromptKey,'yes')
  try{
    // Keep the permission, not an idle capture. A fresh stream is opened only
    // when a call becomes active.
    const stream=await navigator.mediaDevices.getUserMedia({audio:true,video:false})
    stream.getTracks().forEach(track=>track.stop())
    callAudioState.microphone='granted'
    callAudioState.error=''
    if(!await unlockPlayback())callAudioState.error='请点击页面以启用提醒声音'
  }catch(e:any){
    callAudioState.microphone='denied'
    callAudioState.error=e?.name==='NotAllowedError'?'麦克风权限被拒绝，请在浏览器地址栏中允许':'无法初始化麦克风'
  }
}

/** Must be callable from a user gesture to satisfy browser autoplay rules. */
export async function enableCallAudio(){
  if(callAudioState.microphone!=='granted'){
    requested=false
    await requestCallAudioAccess(true)
  }
  await unlockPlayback()
  if('Notification' in window&&Notification.permission==='default'){
    try{await Notification.requestPermission()}catch{}
  }
}

function bellNote(ctx:AudioContext,destination:AudioNode,frequency:number,start:number,duration:number,volume:number){
  const fundamental=ctx.createOscillator()
  const shimmer=ctx.createOscillator()
  const gain=ctx.createGain()
  fundamental.type='sine'
  shimmer.type='triangle'
  fundamental.frequency.setValueAtTime(frequency,start)
  shimmer.frequency.setValueAtTime(frequency*2,start)
  gain.gain.setValueAtTime(.0001,start)
  gain.gain.exponentialRampToValueAtTime(volume,start+.018)
  gain.gain.exponentialRampToValueAtTime(volume*.36,start+duration*.48)
  gain.gain.exponentialRampToValueAtTime(.0001,start+duration)
  fundamental.connect(gain)
  shimmer.connect(gain)
  gain.connect(destination)
  fundamental.start(start)
  shimmer.start(start)
  fundamental.stop(start+duration+.02)
  shimmer.stop(start+duration+.02)
}

function ringtonePhrase(ctx:AudioContext,destination:AudioNode){
  const start=ctx.currentTime+.025
  const notes=[
    [523.25,0,.34,.075],
    [659.25,.28,.36,.07],
    [783.99,.58,.46,.072],
    [1046.5,1.02,.72,.082],
    [783.99,1.92,.34,.068],
    [987.77,2.20,.38,.07],
    [1318.51,2.52,.72,.082],
  ] as const
  for(const [frequency,offset,duration,volume] of notes){
    bellNote(ctx,destination,frequency,start+offset,duration,volume)
  }
}

/** A warm, repeating arpeggio for an active incoming call. */
export function startIncomingRingtone(){
  const ctx=audioContext()
  const master=ctx.createGain()
  master.gain.value=.88
  master.connect(ctx.destination)
  let stopped=false
  let timer:number|undefined
  void unlockPlayback().then(ready=>{
    if(!ready||stopped)return
    ringtonePhrase(ctx,master)
    timer=window.setInterval(()=>ringtonePhrase(ctx,master),3900)
  })
  return ()=>{
    if(stopped)return
    stopped=true
    if(timer)clearInterval(timer)
    master.gain.cancelScheduledValues(ctx.currentTime)
    master.gain.setValueAtTime(0,ctx.currentTime)
    window.setTimeout(()=>master.disconnect(),800)
  }
}

/** A short three-note chime; each new SMS is keyed and played only once. */
export function playMessageNotification(){
  const ctx=audioContext()
  void unlockPlayback().then(ready=>{
    if(!ready)return
    const master=ctx.createGain()
    master.gain.value=.8
    master.connect(ctx.destination)
    const start=ctx.currentTime+.02
    bellNote(ctx,master,659.25,start,.28,.065)
    bellNote(ctx,master,987.77,start+.18,.38,.07)
    bellNote(ctx,master,1318.51,start+.42,.62,.078)
    window.setTimeout(()=>master.disconnect(),1300)
  })
}

const dtmfFrequencies:Record<string,[number,number]>={
  '1':[697,1209],'2':[697,1336],'3':[697,1477],
  '4':[770,1209],'5':[770,1336],'6':[770,1477],
  '7':[852,1209],'8':[852,1336],'9':[852,1477],
  '*':[941,1209],'0':[941,1336],'#':[941,1477],
}

/** Play the standard local dual-frequency feedback for every dial-pad press. */
export function playDTMFKey(key:string,duration=120){
  const frequencies=dtmfFrequencies[key]
  if(!frequencies)return
  const ctx=audioContext()
  void unlockPlayback().then(ready=>{
    if(!ready)return
    const start=ctx.currentTime+.005
    const stop=start+Math.max(60,Math.min(duration,400))/1000
    const gain=ctx.createGain()
    gain.gain.setValueAtTime(.0001,start)
    gain.gain.exponentialRampToValueAtTime(.075,start+.008)
    gain.gain.setValueAtTime(.075,Math.max(start+.008,stop-.025))
    gain.gain.exponentialRampToValueAtTime(.0001,stop)
    gain.connect(ctx.destination)
    for(const frequency of frequencies){
      const oscillator=ctx.createOscillator()
      oscillator.type='sine'
      oscillator.frequency.value=frequency
      oscillator.connect(gain)
      oscillator.start(start)
      oscillator.stop(stop+.01)
    }
    window.setTimeout(()=>gain.disconnect(),duration+100)
  })
}
