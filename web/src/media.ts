import { api, post } from './api'

export interface CallMediaStatus {
  phase:'waiting_for_permission'|'negotiating'|'bridged'|'recovering'|'timeout'|'failed'
  micLevel:number
  packetsSent:number
  bytesSent:number
  error?:string
  timings?:Record<string,string>
}

export interface CallMediaSession {
  disconnect:()=>void
  playTestTone:()=>Promise<void>
}

interface BackendMediaStatus {
  state:'waiting_for_permission'|'negotiating'|'bridged'|'recovering'|'timeout'|'unavailable'
  error?:string
  sessionStartedAt?:string
  audioOpenedAt?:string
  offerReceivedAt?:string
  peerConnectedAt?:string
  firstRtpAt?:string
  firstPcmAt?:string
  lastDisconnectAt?:string
}

export async function connectCallMedia(callId:string, temp=false, onStatus?:(status:CallMediaStatus)=>void):Promise<CallMediaSession> {
  const status:CallMediaStatus={phase:'waiting_for_permission',micLevel:0,packetsSent:0,bytesSent:0}
  const update=(next:Partial<CallMediaStatus>)=>{Object.assign(status,next);onStatus?.({...status})}
  update({phase:'waiting_for_permission'})
  const stream=await navigator.mediaDevices.getUserMedia({audio:{echoCancellation:true,noiseSuppression:true,autoGainControl:true},video:false})
  const pc=new RTCPeerConnection()
  const track=stream.getAudioTracks()[0]
  if(!track)throw new Error('MICROPHONE_TRACK_MISSING')
  track.onended=()=>update({phase:'failed',error:'MICROPHONE_DISCONNECTED'})
  track.onmute=()=>update({phase:'recovering',error:'MICROPHONE_MUTED_BY_BROWSER'})
  track.onunmute=()=>update({phase:status.phase==='bridged'?'bridged':'negotiating',error:undefined})
  const context=new AudioContext()
  await context.resume()
  const microphone=context.createMediaStreamSource(stream)
  const outgoing=context.createMediaStreamDestination()
  microphone.connect(outgoing)
  const outgoingTrack=outgoing.stream.getAudioTracks()[0]
  if(!outgoingTrack)throw new Error('OUTGOING_AUDIO_TRACK_MISSING')
  pc.addTrack(outgoingTrack,outgoing.stream)
  const audio=document.createElement('audio')
  audio.autoplay=true
  pc.ontrack=e=>{audio.srcObject=e.streams[0]||new MediaStream([e.track]);audio.play().catch(()=>{})}
  let disconnectTimer:number|undefined
  pc.onconnectionstatechange=()=>{
    if(pc.connectionState==='connected'){
      if(disconnectTimer)clearTimeout(disconnectTimer)
      disconnectTimer=undefined
      if(status.phase!=='bridged')update({phase:'negotiating',error:undefined})
    }else if(pc.connectionState==='disconnected'){
      update({phase:'recovering',error:'WEBRTC_DISCONNECTED'})
      if(disconnectTimer)clearTimeout(disconnectTimer)
      disconnectTimer=window.setTimeout(()=>{
        if(pc.connectionState==='disconnected')update({phase:'failed',error:'WEBRTC_RECOVERY_TIMEOUT'})
      },5000)
    }else if(pc.connectionState==='failed'){
      update({phase:'failed',error:'WEBRTC_FAILED'})
    }else if(pc.connectionState!=='closed'){
      update({phase:'negotiating'})
    }
  }
  const analyser=context.createAnalyser()
  analyser.fftSize=256
  microphone.connect(analyser)
  const levels=new Uint8Array(analyser.frequencyBinCount)
  const meter=window.setInterval(async()=>{
    analyser.getByteTimeDomainData(levels)
    let sum=0
    for(const sample of levels){const normalized=(sample-128)/128;sum+=normalized*normalized}
    const micLevel=Math.min(100,Math.round(Math.sqrt(sum/levels.length)*240))
    let packetsSent=0,bytesSent=0
    const reports=await pc.getStats()
    reports.forEach(report=>{if(report.type==='outbound-rtp'&&report.kind==='audio'){packetsSent=report.packetsSent||0;bytesSent=report.bytesSent||0}})
    update({micLevel,packetsSent,bytesSent})
  },500)
  update({phase:'negotiating'})
  let stopped=false
  let backendPoll:number|undefined
  const cleanup=()=>{if(stopped)return;stopped=true;clearInterval(meter);if(backendPoll)clearInterval(backendPoll);if(disconnectTimer)clearTimeout(disconnectTimer);stream.getTracks().forEach(t=>t.stop());outgoing.stream.getTracks().forEach(t=>t.stop());pc.close();audio.remove();void context.close()}
  const playTestTone=async()=>{
    if(stopped||pc.connectionState!=='connected')throw new Error('MEDIA_NOT_CONNECTED')
    await context.resume()
    const oscillator=context.createOscillator()
    const gain=context.createGain()
    oscillator.type='sine'
    oscillator.frequency.value=1000
    gain.gain.setValueAtTime(0,context.currentTime)
    gain.gain.linearRampToValueAtTime(.16,context.currentTime+.03)
    gain.gain.setValueAtTime(.16,context.currentTime+1.7)
    gain.gain.linearRampToValueAtTime(0,context.currentTime+1.95)
    oscillator.connect(gain).connect(outgoing)
    oscillator.start()
    oscillator.stop(context.currentTime+2)
    await new Promise(resolve=>setTimeout(resolve,2050))
    oscillator.disconnect()
    gain.disconnect()
  }
  try{
    const offer=await pc.createOffer()
    await pc.setLocalDescription(offer)
    await new Promise<void>((resolve,reject)=>{
      if(pc.iceGatheringState==='complete')return resolve()
      const timer=setTimeout(()=>reject(new Error('ICE_TIMEOUT')),10000)
      pc.onicegatheringstatechange=()=>{if(pc.iceGatheringState==='complete'){clearTimeout(timer);resolve()}}
    })
    const base=temp?'/api/v1/temp/call':'/api/v1/calls'
    const answer=await post<{sdp:string;type:RTCSdpType;media?:BackendMediaStatus}>(`${base}/${callId}/media`,{sdp:pc.localDescription!.sdp,type:'offer'})
    await pc.setRemoteDescription(answer)
    const syncBackend=async()=>{
      if(stopped)return
      try{
        const backend=await api<BackendMediaStatus>(`${base}/${callId}/media/status`)
        const timings:Record<string,string>={}
        for(const [key,value] of Object.entries(backend)){
          if(key.endsWith('At')&&typeof value==='string')timings[key]=value
        }
        // The backend only reports bridged after it has received browser
        // audio and written it to the live call. This is more reliable than
        // Safari/WebView's sometimes stale RTCPeerConnection state.
        if(backend.state==='bridged')update({phase:'bridged',error:undefined,timings})
        else if(backend.state==='timeout')update({phase:'timeout',error:backend.error||'WEB_MEDIA_TIMEOUT',timings})
        else if(backend.state==='unavailable')update({phase:'failed',error:backend.error||'MEDIA_UNAVAILABLE',timings})
        else if(backend.state==='recovering')update({phase:'recovering',error:backend.error,timings})
        else if(status.phase!=='bridged')update({phase:backend.state,error:backend.error,timings})
      }catch{/* The call state stream remains authoritative during short HTTP interruptions. */}
    }
    await syncBackend()
    backendPoll=window.setInterval(()=>void syncBackend(),250)
    return {disconnect:cleanup,playTestTone}
  }catch(error){
    cleanup()
    throw error
  }
}
