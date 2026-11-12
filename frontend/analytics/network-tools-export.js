// Export only the selected immutable measurement snapshot. Canvas drawing does
// not capture the page, load remote media, or upload account/measurement data.
export async function downloadMeasurementImage(session, rendered, hideSensitive, canContinue = () => true) {
  const styles = getComputedStyle(document.documentElement);
  const theme = Object.fromEntries(["ink", "soft-ink", "paper", "border", "tint"].map(key => [key, styles.getPropertyValue(`--${key}`).trim()]));
  const family = getComputedStyle(document.body).fontFamily;
  const req = session.request, p = req.parameters;
  const tool = {ping:"Ping",traceroute:"NextTrace",mtr:"NextTrace MTR",iperf3:"iperf3"}[p.tool];
  const states = {running:"执行中",preparing:"准备中",ready:"已就绪",stopping:"停止中",cleanup_pending:"等待确认资源回收",succeeded:"测量结束",failed:"失败",canceled:"已停止"};
  const state = value => states[value] || value;
  const clean = value => {
    let result = String(value);
    if (hideSensitive) for (const ep of [req.node1,req.node2]) {
      for (const secret of [ep.name,ep.address,ep.agent_id,ep.instance_id].filter(Boolean)) result=result.split(secret).join("已隐藏");
    }
    return result;
  };
  const blocks = [];
  blocks.push({type:"heading",title:tool,detail:p.protocol.toUpperCase(),status:state(session.state),time:new Date(session.created_at).toLocaleString("zh-CN"),private:hideSensitive});
  const parameters = p.tool === "iperf3"
    ? [["测试时长",`${p.duration} s`],["并发流",String(p.streams)],["目标端口",String(p.port)],[p.protocol==="udp"?"UDP 数据报":"缓冲区",`${p.size} 字节`]]
    : [[p.tool==="mtr"?"轮数":p.tool==="traceroute"?"每跳探测":"探测次数",String(p.count)],["间隔",`${p.interval_ms} ms`],["单次超时",`${p.timeout_ms} ms`],[p.tool==="ping"?"ICMP 负载":"总包长",`${p.size} 字节`],
      ...(["traceroute","mtr"].includes(p.tool)?[["最大跳数",String(p.max_hops)],["IP 查询提供方",p.provider],...(p.protocol!=="icmp"?[["目标端口",String(p.port)]]:[])]:[])];
  if (session.byte_limit>0) parameters.push(["共享流量预算",`${session.byte_limit/1048576} MiB`]);
  blocks.push({type:"facts",items:parameters});
  const measurements=Object.values(session.results || {}).filter(r=>!r.id.endsWith("-server"));
  for (const r of measurements) {
    const reverse=r.id.endsWith("-reverse");
    const from=reverse?req.node2:req.node1,to=reverse?req.node1:req.node2;
    const endpoint=(ep,label)=>hideSensitive?[label]:[...new Set([ep.name,ep.address].filter(Boolean))];
    blocks.push({type:"direction",title:reverse?"回程":"去程",status:r.stage==="installing"?"正在安装诊断工具":state(r.state)});
    blocks.push({type:"endpoints",items:[
      ["来源",endpoint(from,reverse?"节点2":"节点1")],
      ["目标",endpoint(to,reverse?"节点1":req.target_mode==="ip"?"自定义 IP":"节点2")],
    ]});
    const host=rendered.querySelector(`[data-measurement-result="${CSS.escape(r.id)}"] [data-export-output]`);
    // Read all DOM data and semantic colors before any asynchronous font work.
    // Refreshing the measurement cannot alter this fixed export snapshot.
    const pathStatus=host?.querySelector(".a-tools-path-status");
    if(pathStatus) blocks.push({type:"note",parts:[...pathStatus.children].map(el=>({text:clean(el.innerText.trim()),color:getComputedStyle(el).color}))});
    const metrics=[...host?.querySelectorAll(".a-tools-metrics > div") || []].map(el=>({
      label:el.querySelector("dt").textContent.trim(), value:el.querySelector(".a-tools-metric-value").textContent.trim(), unit:el.querySelector(".a-tools-metric-unit").textContent.trim(),
    }));
    if(metrics.length) blocks.push({type:"metrics",items:metrics});
    if (r.error && !hideSensitive) blocks.push({type:"note",parts:[{text:clean(r.error),color:theme["soft-ink"]}]});
    const tables=[...host?.querySelectorAll("table") || []];
    for (const table of tables) {
      const headings=[...table.querySelectorAll("thead th")].map(c=>c.textContent.trim());
      const rows=[...table.querySelectorAll("tbody tr")].map(row=>[...row.cells].map((cell,col)=>{
        // Address cells can contain reverse DNS and location metadata. Remove
        // the entire cell in privacy mode, including those secondary lines.
        if(hideSensitive && ["地址","响应地址","响应主机"].includes(headings[col])) return [{text:cell.textContent.trim()==="*"?"*":"已隐藏",color:theme["soft-ink"]}];
        const stack=cell.firstElementChild?.classList.contains("a-stack")?cell.firstElementChild:null;
        return [...(stack?stack.children:[cell])].map(el=>({text:clean(el.innerText.trim()),color:getComputedStyle(el.querySelector('[class*="a-trace-"]') || el).color}));
      }));
      const title=p.tool==="ping"?"延迟统计":p.tool==="traceroute"?"路由明细":p.tool==="mtr"?"逐跳统计":headings[0].includes("时间段")?"测速过程":"发送端与接收端汇总";
      blocks.push({type:"table",title,headings,rows});
    }
    if(!tables.length && !metrics.length) blocks.push({type:"note",parts:[{text:r.stage==="installing"?"正在安装诊断工具":r.state==="running"?"等待探测结果":"尚无结果",color:theme["soft-ink"]}]});
  }
  if(!measurements.length) blocks.push({type:"note",parts:[{text:session.state==="preparing"?"等待 Agent 执行":"尚无结果",color:theme["soft-ink"]}]});
  if(p.tool==="iperf3" && req.target_mode!=="ip") {
    const server=session.results?.[session.id+"-server"];
    if(server) blocks.push({type:"note",parts:[{text:server.cleaned?"服务端已回收":["succeeded","failed","canceled","cleanup_pending","stopping"].includes(session.state)?"服务端回收尚未确认":server.state==="ready"?"服务端已就绪":"服务端准备中",color:theme["soft-ink"]}]});
  }
  await document.fonts.ready;
  const width=1600,pad=48,content=width-2*pad;
  const canvas=document.createElement("canvas"), ctx=canvas.getContext("2d");
  if(!ctx) throw new Error("浏览器无法生成图片");
  const font=(size,weight=400)=>`${weight} ${size}px ${family}`;
  const wrap=(value,maxWidth,size,weight=400)=>{
    ctx.font=font(size,weight);const lines=[];
    for(const part of String(value).split("\n")) {
      let current="";
      for(const char of part) {if(current && ctx.measureText(current+char).width>maxWidth){lines.push(current);current="";}current+=char;}
      lines.push(current);
    }
    return lines;
  };
  const text=(value,x,y,size=16,color=theme.ink,weight=400)=>{ctx.font=font(size,weight);ctx.fillStyle=color;ctx.fillText(String(value),x,y);};
  const rule=y=>{ctx.strokeStyle=theme.border;ctx.lineWidth=1;ctx.beginPath();ctx.moveTo(pad,y+.5);ctx.lineTo(width-pad,y+.5);ctx.stroke();};
  let height=pad;
  // Measure every row before allocating the final bitmap; nothing is cropped.
  for(const block of blocks) {
    block.y=height;
    switch(block.type) {
      case "heading": block.height=104;break;
      case "facts":
        block.columns=4;block.cellWidth=content/4;
        block.items=block.items.map(([label,value])=>({label,lines:wrap(value,block.cellWidth-24,16)}));
        block.rowHeights=[];
        for(let i=0;i<block.items.length;i+=4) block.rowHeights.push(28+Math.max(...block.items.slice(i,i+4).map(item=>item.lines.length))*24);
        block.height=block.rowHeights.reduce((a,b)=>a+b,0)+24;break;
      case "direction":block.height=62;break;
      case "endpoints":
        block.items=block.items.map(([label,lines])=>({label,lines:lines.flatMap((line,i)=>wrap(line,content/2-32,i?14:19,i?400:600).map(text=>({text,secondary:i>0})))}));
        block.height=32+Math.max(...block.items.map(item=>item.lines.length))*28+18;break;
      case "metrics":
        block.cellWidth=content/block.items.length;
        block.items=block.items.map(item=>({...item,lines:wrap(item.value,block.cellWidth-92,32,600)}));
        block.height=56+Math.max(...block.items.map(item=>item.lines.length))*40+26;break;
      case "note":
        block.lines=block.parts.flatMap(part=>wrap(part.text,content,15).map(text=>({text,color:part.color})));
        block.height=block.lines.length*24+20;break;
      case "table": {
        const weights=block.headings.map(h=>h==="跳数"?.55:h==="归属"?3.6:h==="响应主机"?3.2:["响应地址","网络"].includes(h)?1.8:h.includes("延迟")?2.5:h==="ASN"?1:h.includes("时间段")?1.6:1.25);
        const total=weights.reduce((a,b)=>a+b,0);
        block.widths=weights.map(w=>content*w/total);
        block.grid=[block.headings.map(text=>[{text,color:theme["soft-ink"]}]),...block.rows].map((row,index)=>{
          const cells=row.map((parts,i)=>({lines:parts.flatMap(part=>wrap(part.text,block.widths[i]-24,index?16:14,index?400:600).map(text=>({text,color:part.color})))}));
          return {cells,height:Math.max(...cells.map(c=>c.lines.length),1)*24+24};
        });
        block.height=42+block.grid.reduce((sum,row)=>sum+row.height,0)+28;break;
      }
    }
    height+=block.height;
  }
  height+=24;
  if(height>30000) throw new Error("结果过长，无法导出为单张图片");
  canvas.width=width;canvas.height=height;
  ctx.fillStyle=theme.paper;ctx.fillRect(0,0,width,height);ctx.textBaseline="top";
  for(const block of blocks) {
    const y=block.y;
    switch(block.type) {
      case "heading":
        text(block.title,pad,y,34,theme.ink,600);
        ctx.font=font(34,600);const titleWidth=ctx.measureText(block.title).width;
        text(block.detail,pad+titleWidth+24,y+14,16,theme["soft-ink"]);
        ctx.font=font(16);
        text(block.status,width-pad-ctx.measureText(block.status).width,y+14,16,theme["soft-ink"]);
        text(block.time,pad,y+56,15,theme["soft-ink"]);
        if(block.private) text("已脱敏",width-pad-70,y+56,15,theme["soft-ink"]);
        rule(y+86);break;
      case "facts": {
        let rowY=y;
        block.items.forEach((item,i)=>{if(i>0 && i%4===0) rowY+=block.rowHeights[i/4-1];const x=pad+(i%4)*block.cellWidth;text(item.label,x,rowY,13,theme["soft-ink"]);item.lines.forEach((line,j)=>text(line,x,rowY+23+j*24));});
        rule(y+block.height-8);break;
      }
      case "direction":text(block.title,pad,y+18,22,theme.ink,600);text(block.status,pad+76,y+24,15,theme["soft-ink"]);break;
      case "endpoints":
        block.items.forEach((item,i)=>{const x=pad+i*content/2;text(item.label,x,y,13,theme["soft-ink"]);item.lines.forEach((line,j)=>text(line.text,x,y+25+j*28,line.secondary?14:19,line.secondary?theme["soft-ink"]:theme.ink,line.secondary?400:600));});break;
      case "metrics":
        rule(y);
        block.items.forEach((item,i)=>{const x=pad+i*block.cellWidth;text(item.label,x,y+18,14,theme["soft-ink"]);item.lines.forEach((line,j)=>text(line,x,y+46+j*40,32,theme.ink,600));ctx.font=font(32,600);const valueWidth=ctx.measureText(item.lines.at(-1)).width;text(item.unit,x+valueWidth+10,y+59+(item.lines.length-1)*40,14,theme["soft-ink"]);});
        rule(y+block.height-18);break;
      case "note":block.lines.forEach((line,i)=>text(line.text,pad,y+i*24,15,line.color));break;
      case "table": {
        text(block.title,pad,y,17,theme.ink,600);
        let rowY=y+36;
        block.grid.forEach((row,index)=>{
          if(index===0 || index%2===0){ctx.fillStyle=theme.tint || theme.paper;ctx.fillRect(pad,rowY,content,row.height);}
          let x=pad;
          row.cells.forEach((cell,i)=>{cell.lines.forEach((line,j)=>text(line.text,x+12,rowY+12+j*24,index?16:14,line.color,index?400:600));x+=block.widths[i];});
          rowY+=row.height;rule(rowY);
        });break;
      }
    }
  }
  const blob=await new Promise(resolve=>canvas.toBlob(resolve,"image/png"));
  if(!blob) throw new Error("图片生成失败");
  if(!canContinue()) return;
  const url=URL.createObjectURL(blob),link=document.createElement("a");
  link.href=url;link.download=`${p.tool}-${new Date(session.created_at).toISOString().replace(/[:.]/g,"-")}${hideSensitive?"-redacted":""}.png`;
  link.click();setTimeout(()=>URL.revokeObjectURL(url),30000);
}
