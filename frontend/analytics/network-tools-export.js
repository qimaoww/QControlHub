// Export only the selected immutable measurement snapshot, never the page or
// account/navigation DOM. Nothing is uploaded or retained by the browser.
export async function downloadMeasurementImage(session, rendered, hideSensitive, canContinue = () => true) {
  const styles = getComputedStyle(document.documentElement);
  const ink = styles.getPropertyValue("--ink").trim();
  const soft = styles.getPropertyValue("--soft-ink").trim();
  const paper = styles.getPropertyValue("--paper").trim();
  const border = styles.getPropertyValue("--border").trim();
  const req = session.request, p = req.parameters;
  const tool = {ping:"Ping",traceroute:"NextTrace",mtr:"NextTrace MTR",iperf3:"iperf3"}[p.tool];
  const states = {running:"执行中",preparing:"准备中",ready:"已就绪",stopping:"停止中",cleanup_pending:"等待确认资源回收",succeeded:"测量结束",failed:"失败",canceled:"已停止"};
  // All cells are captured synchronously before font loading or PNG encoding;
  // polling and form changes cannot change this export underneath the user.
  const blocks = [];
  const text = (value, size=16, color=ink) => blocks.push({text:String(value),size,color});
  const clean = value => {
    let result = String(value);
    if (!hideSensitive) return result;
    for (const ep of [req.node1,req.node2]) {
      for (const secret of [ep.name,ep.address,ep.agent_id,ep.instance_id].filter(Boolean)) result=result.split(secret).join("已隐藏");
    }
    return result;
  };
  text(`${tool} · ${p.protocol.toUpperCase()} · ${states[session.state] || session.state}`,24);
  text(`测量时间：${new Date(session.created_at).toLocaleString("zh-CN")}`,14,soft);
  text(p.tool==="iperf3" ? `${p.duration} s · ${p.streams} 流 · ${p.size} 字节` : `${p.count} ${p.tool==="mtr"?"轮":p.tool==="traceroute"?"次 / 跳":"次"} · ${p.interval_ms} ms 间隔 · ${p.timeout_ms} ms 超时 · ${p.size} 字节${p.tool==="ping"?"负载":`总包长 · ${p.max_hops} 跳 · ${p.provider}`}`,14,soft);
  if (session.byte_limit > 0) text(`共享流量预算：${session.byte_limit/1048576} MiB`,14,soft);
  if (hideSensitive) text("节点名称、IP、主机名及诊断原文已隐藏",14,soft);
  const measurements=Object.values(session.results || {}).filter(r=>!r.id.endsWith("-server"));
  for (const [i,r] of measurements.entries()) {
    const reverse=r.id.endsWith("-reverse");
    const from=reverse?req.node2:req.node1,to=reverse?req.node1:req.node2;
    text(`${reverse?"回程":"去程"} · ${states[r.state] || r.state}`,20);
    const endpoint=(ep,label)=>hideSensitive?label:[...new Set([ep.name,ep.address].filter(Boolean))].join(" · ");
    text(`来源：${endpoint(from,reverse?"节点2":"节点1")}`,16);
    text(`目标：${endpoint(to,reverse?"节点1":req.target_mode==="ip"?"自定义 IP":"节点2")}`,16);
    if (["traceroute","mtr"].includes(p.tool)) {
      const reason=r.output?.StopReason?.reason || r.output?.path_end?.reason;
      const outcome={destination_reached:"目标已响应",unreachable:"路径返回不可达",max_hops:"已达最大跳数，未确认目标可达"}[reason];
      text(outcome || (r.state==="running"?"正在探测":"未确认目标可达"),14,soft);
    }
    if (r.error && !hideSensitive) text(r.error,14,soft);
    const table=rendered.querySelector(`[data-measurement-result="${CSS.escape(r.id)}"] table`);
    if (!table) {text("尚无结果",16,soft);continue;}
    const headings=[...table.querySelectorAll("thead th")].map(c=>c.textContent.trim());
    const rows=[...table.querySelectorAll("tbody tr")].map(row=>[...row.cells].map((cell,col)=>{
      const colored=cell.querySelector('[class*="a-trace-"]');
      const color=colored?getComputedStyle(colored).color:ink;
      const heading=headings[col];
      // Address columns may include reverse-DNS names on another line.
      const value=hideSensitive && ["地址","响应地址","响应主机"].includes(heading) ? (cell.textContent.trim()==="*"?"*":"已隐藏") : clean(cell.textContent.trim());
      return {text:value,color};
    }));
    blocks.push({headings,rows});
  }
  if (!measurements.length) text("尚无结果",16,soft);
  await document.fonts.ready;
  const width=1600,pad=40,line=25;
  const canvas=document.createElement("canvas"), context=canvas.getContext("2d");
  if (!context) throw new Error("浏览器无法生成图片");
  const font=size=>`${size}px ${getComputedStyle(document.body).fontFamily}`;
  const wrap=(value,maxWidth,size)=>{
    context.font=font(size);const lines=[];
    for (const part of String(value).split("\n")) {
      let current="";
      for (const char of part) {if (current && context.measureText(current+char).width>maxWidth){lines.push(current);current="";}current+=char;}
      lines.push(current);
    }
    return lines;
  };
  let height=pad;
  for (const block of blocks) {
    block.y=height;
    if (block.text!=null) {block.lines=wrap(block.text,width-2*pad,block.size);height+=block.lines.length*(block.size+10)+12;continue;}
    const weights=block.headings.map(h=>["跳数","TTL"].includes(h)?0.5:["归属","ASN / 归属"].includes(h)?3:h === "响应主机" ? 3 : ["地址","响应地址","网络"].includes(h)?1.65:h.includes("延迟")?2.2:1.3);
    const total=weights.reduce((a,b)=>a+b,0);
    block.widths=weights.map(w=>(width-2*pad)*w/total);
    block.grid=[block.headings.map(text=>({text,color:soft})),...block.rows].map(row=>{
      const cells=row.map((cell,i)=>({...cell,lines:wrap(cell.text,block.widths[i]-24,15)}));
      return {cells,height:Math.max(...cells.map(c=>c.lines.length),1)*line+20};
    });
    height+=block.grid.reduce((sum,row)=>sum+row.height,0)+28;
  }
  height+=pad;
  // Bound memory and browser canvas dimensions. Never silently crop hop rows.
  if (height>30000) throw new Error("结果过长，无法导出为单张图片");
  canvas.width=width;canvas.height=height;
  context.fillStyle=paper;context.fillRect(0,0,width,height);
  context.textBaseline="top";
  for (const block of blocks) {
    if(block.text!=null){context.font=font(block.size);context.fillStyle=block.color;block.lines.forEach((t,i)=>context.fillText(t,pad,block.y+i*(block.size+10)));continue;}
    let y=block.y;
    for(const row of block.grid){let x=pad;context.font=font(15);for(const [i,cell] of row.cells.entries()){context.fillStyle=cell.color;cell.lines.forEach((t,j)=>context.fillText(t,x+12,y+10+j*line));x+=block.widths[i];}y+=row.height;context.strokeStyle=border;context.beginPath();context.moveTo(pad,y);context.lineTo(width-pad,y);context.stroke();}
  }
  const blob=await new Promise(resolve=>canvas.toBlob(resolve,"image/png"));
  if(!blob) throw new Error("图片生成失败");
  if (!canContinue()) return;
  const url=URL.createObjectURL(blob),link=document.createElement("a");
  link.href=url;link.download=`${p.tool}-${new Date(session.created_at).toISOString().replace(/[:.]/g,"-")}${hideSensitive?"-redacted":""}.png`;
  link.click();setTimeout(()=>URL.revokeObjectURL(url),30000);
}
