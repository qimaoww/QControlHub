import { loadingTable, readRegion, dialogContext } from "./loading.js";
import { node, button, actions, stack, table, muted, list, pathID } from './ui.js';
import { bytes, date } from './operations.js';

function metricChart(samples, width, name) {
  const chart=document.createElementNS('http://www.w3.org/2000/svg','svg');
  const right=width-20;
  chart.setAttribute('viewBox',`0 0 ${width} 240`);
  chart.setAttribute('role','img');chart.setAttribute('class','a-chart');
  chart.setAttribute('aria-label',`${name} 最近 24 小时网络速率`);
  const add=(tag,attrs,text)=>{
    const part=document.createElementNS(chart.namespaceURI,tag);
    for(const [key,value]of Object.entries(attrs))part.setAttribute(key,String(value));
    if(text)part.textContent=text;chart.append(part);
  };
  const start=Date.parse(samples[0].sampled_at),end=Date.parse(samples.at(-1).sampled_at);
  const max=Math.max(1,...samples.map(sample=>Math.max(sample.rx_rate_bps,sample.tx_rate_bps)));
  for(let i=0;i<4;i++){
    const y=16+i*64;
    add('path',{d:`M76 ${y}H${right}`,stroke:'#e4e4e7','stroke-dasharray':'3 4'});
    add('text',{x:68,y:y+4,'text-anchor':'end'},`${bytes(max*(1-i/3))}/s`);
  }
  for(const [key,color,dash]of [['rx_rate_bps','#27272a',''],['tx_rate_bps','#71717a','5 4']]){
    const d=samples.map((sample,index)=>`${index?'L':'M'}${76+(Date.parse(sample.sampled_at)-start)/Math.max(1,end-start)*(right-76)} ${208-Number(sample[key])/max*192}`).join(' ');
    add('path',{d,stroke:color,'stroke-width':2,'stroke-dasharray':dash,fill:'none'});
  }
  const time=value=>new Date(value).toLocaleTimeString('zh-CN',{hour:'2-digit',minute:'2-digit'});
  add('text',{x:76,y:234},time(samples[0].sampled_at));
  add('text',{x:right,y:234,'text-anchor':'end'},time(samples.at(-1).sampled_at));
  return chart;
}

export function nodeMetrics(ctx, agent) {
  const content=node('div',{className:'a-stack'},muted('最近 24 小时 · 接收与发送速率'),loadingTable(['采样时间','CPU','内存','接收速率','发送速率'],'节点历史指标'));
  let box, sequence=0, observer;
  const stop=()=>observer?.disconnect();
  ctx.signal.addEventListener('abort',stop,{once:true});
  async function read() {
    const request=++sequence;
    const samples=list(await local.api(`/metrics/${pathID(agent.id)}`));
    if(!ctx.current()||request!==sequence||!box.open)return;
    stop();
    const summary=muted('最近 24 小时 · 接收与发送速率');
    if(!samples.length){content.replaceChildren(summary,muted('暂无指标记录'));return;}
    const graph=node('div',{className:'a-chart-container'});
    const draw=width=>graph.replaceChildren(metricChart(samples,Math.max(320,width),agent.name));
    content.replaceChildren(summary,graph,muted('实线：接收 · 虚线：发送'),table(['采样时间','CPU','内存','接收速率','发送速率'],[...samples].reverse().map(sample=>[date(sample.sampled_at),`${sample.cpu_percent.toFixed(1)}%`,`${sample.memory_percent.toFixed(1)}%`,`${bytes(sample.rx_rate_bps)}/s`,`${bytes(sample.tx_rate_bps)}/s`]),{label:'节点历史指标',paginate:24}));
    draw(graph.getBoundingClientRect().width);
    observer=new ResizeObserver(entries=>{if(ctx.current()&&box.open)draw(entries[0].contentRect.width);});
    observer.observe(graph);
  }
  box=ctx.dialog(`${agent.name} · 指标历史`,stack(content),{wide:true});
  box.addEventListener('close',stop,{once:true});
  const local = dialogContext(ctx, box);
  const region = readRegion(local, '历史指标', read, () => {});
  content.before(region.element);
  const refresh = button('刷新指标', () => void load());
  async function load() {
    refresh.disabled = true;
    try { await region.load(); }
    finally { if (local.current()) refresh.disabled = false; }
  }
  box.querySelector('.a-dialog-body').append(actions(refresh));
  void load();
}
