/* shared helpers for the per-project identity scripts (no dependencies) */
(function(){'use strict';
var d=document,R=d.documentElement;
var FX=window.FX={
 reduce:!!(window.matchMedia&&matchMedia('(prefers-reduced-motion: reduce)').matches),
 $:function(s,c){return(c||d).querySelector(s)},
 $$:function(s,c){return Array.prototype.slice.call((c||d).querySelectorAll(s))},
 mouse:{x:-1e3,y:-1e3,px:.5,py:.5},
 /* add .rv and reveal on scroll */
 reveal:function(sel,cls){cls=cls||'in';var els=FX.$$(sel);els.forEach(function(e){e.classList.add('rv')});
  if(FX.reduce||!('IntersectionObserver' in window)){els.forEach(function(e){e.classList.add(cls)});return}
  var io=new IntersectionObserver(function(es){es.forEach(function(x){if(x.isIntersecting){x.target.classList.add(cls);io.unobserve(x.target)}})},{rootMargin:'0px 0px -6% 0px'});els.forEach(function(e,i){e.style.setProperty('--i',i%6);io.observe(e)})},
 /* canvas loop: sizes to its box, pauses offscreen / hidden tab; draw(ctx,w,h,t,dt,m) with m = mouse in canvas px */
 loop:function(cv,draw,o){o=o||{};var ctx=cv.getContext('2d'),w=0,h=0,dpr=1,raf=0,on=false,vis=true,last=0,t0=performance.now(),m={x:-1e3,y:-1e3,in:false};
  function size(){var r=cv.getBoundingClientRect();w=r.width;h=r.height;dpr=Math.min(o.dpr||2,window.devicePixelRatio||1);cv.width=Math.max(1,Math.round(w*dpr));cv.height=Math.max(1,Math.round(h*dpr));ctx.setTransform(dpr,0,0,dpr,0,0);if(o.resize)o.resize(w,h)}
  function fr(now){if(!on)return;var t=(now-t0)/1000,dt=Math.min(.05,(now-last)/1000||.016);last=now;ctx.setTransform(dpr,0,0,dpr,0,0);draw(ctx,w,h,t,dt,m);raf=requestAnimationFrame(fr)}
  function go(){if(on||FX.reduce||d.hidden||!vis)return;on=true;last=performance.now();raf=requestAnimationFrame(fr)}
  function stop(){on=false;cancelAnimationFrame(raf)}
  size();addEventListener('resize',function(){size();if(!on){ctx.setTransform(dpr,0,0,dpr,0,0);draw(ctx,w,h,0,0,m)}});
  var host=o.host||cv.parentElement;host.addEventListener('pointermove',function(e){var r=cv.getBoundingClientRect();m.x=e.clientX-r.left;m.y=e.clientY-r.top;m.in=true},{passive:true});host.addEventListener('pointerleave',function(){m.in=false;m.x=m.y=-1e3});
  if(FX.reduce){draw(ctx,w,h,o.still||2,0,m)}else{
   if('IntersectionObserver' in window)new IntersectionObserver(function(es){vis=es[0].isIntersecting;vis?go():stop()},{threshold:0}).observe(cv);else go();
   d.addEventListener('visibilitychange',function(){d.hidden?stop():go()});draw(ctx,w,h,0,0,m)}
  return{size:size,draw:function(){draw(ctx,w,h,(performance.now()-t0)/1000,0,m)},ctx:ctx};},
 /* create an absolutely positioned canvas as the first child of host */
 layer:function(host,cls){var cv=d.createElement('canvas');cv.className='fx '+(cls||'');cv.setAttribute('aria-hidden','true');cv.setAttribute('role','presentation');if(getComputedStyle(host).position==='static')host.style.position='relative';host.insertBefore(cv,host.firstChild);return cv},
 css:function(n){return getComputedStyle(R).getPropertyValue(n).trim()},
 rnd:function(a,b){return a+Math.random()*(b-a)},
 theme:function(){return R.getAttribute('data-theme')==='dark'?'dark':'light'}
};
addEventListener('pointermove',function(e){FX.mouse.x=e.clientX;FX.mouse.y=e.clientY;FX.mouse.px=e.clientX/innerWidth;FX.mouse.py=e.clientY/innerHeight},{passive:true});
R.classList.add('fxjs');
})();

(function(){var sky=FX.$('.sky');if(!sky)return;var cv=FX.layer(sky),P=[],grains=[],walk=0,state=0,pause=0,ox=0;
function dark(){return FX.theme()==='dark'}
function dune(c,w,y0,amp,fr,sp,t,col,par){c.fillStyle=col;c.beginPath();c.moveTo(0,y0+amp+400);for(var x=0;x<=w+8;x+=8){var y=y0+Math.sin(x*fr+t*sp+par)*amp+Math.sin(x*fr*2.3+t*sp*1.6)*amp*.35;c.lineTo(x,y)}c.lineTo(w,y0+amp+400);c.closePath();c.fill()}
function yAt(x,y0,amp,fr,sp,t,par){return y0+Math.sin(x*fr+t*sp+par)*amp+Math.sin(x*fr*2.3+t*sp*1.6)*amp*.35}
function css(n){return getComputedStyle(document.documentElement).getPropertyValue(n).trim()}
FX.loop(cv,function(c,w,h,t,dt,m){c.clearRect(0,0,w,h);var D=dark(),mx=(FX.mouse.px-.5)*2,base=h*(w<700?.9:.84);ox+=(mx-ox)*.04;
 /* sun / moon orb */
 var sx=w*(w<700?.8:.84)+ox*-30,sy=h*(w<700?.08:.2)+Math.sin(t*.1)*10,R=w<700?26:Math.min(w,900)*.07;
 var g=c.createRadialGradient(sx,sy,R*.2,sx,sy,R*3.2);g.addColorStop(0,D?'rgba(246,185,74,.35)':'rgba(255,240,200,.9)');g.addColorStop(1,'rgba(255,200,150,0)');c.fillStyle=g;c.fillRect(0,0,w,h);
 c.fillStyle=D?'#f6e4b0':'#fff6e0';c.beginPath();c.arc(sx,sy,R,0,6.3);c.fill();
 if(D){c.fillStyle='#130e2a';c.beginPath();c.arc(sx+R*.38,sy-R*.1,R*.88,0,6.3);c.fill()}
 /* back dunes */
 dune(c,w,base-h*.07,18,.0038,.18,t,D?'#2a1f4d':'#f19a79',0);
 dune(c,w,base,26,.0028,.12,t,css('--dune1'),2);
 /* caravan trail on the mid dune: dots appear as it walks, it stops, then resumes */
 var spd=state===0?60:0;if(state===0){walk+=spd*dt;if(walk>w*.12&&Math.random()<dt*.12){state=1;pause=FX.rnd(1.2,2.2)}}else{pause-=dt;if(pause<=0)state=0}
 if(walk>w*1.05)walk=0;
 for(var x=0;x<walk;x+=18){var yy=yAt(x,base,26,.0028,.12,t,2)-4;c.fillStyle=D?'rgba(246,185,74,'+(.15+.5*(x/walk))+')':'rgba(60,20,10,'+(.1+.4*(x/walk))+')';c.beginPath();c.arc(x,yy,2.2,0,6.3);c.fill()}
 var cy=yAt(walk,base,26,.0028,.12,t,2)-10;c.fillStyle=D?'#f6b94a':'#3a1a0e';c.beginPath();c.ellipse(walk,cy-6,9,6,0,0,6.3);c.fill();c.fillRect(walk-8,cy-2,2,9);c.fillRect(walk+5,cy-2,2,9);c.beginPath();c.arc(walk+9,cy-12,3,0,6.3);c.fill();
 if(state===1){c.font='600 11px ui-monospace,monospace';c.fillText('· · ·',walk-10,cy-24)}
 dune(c,w,base+h*.08,34,.0021,.09,t,css('--dune2'),4);
 /* wind-blown grains, swirled by the cursor */
 if(grains.length<(w<600?60:140))grains.push({x:-10,y:FX.rnd(h*.15,h*.8),v:FX.rnd(40,140),s:FX.rnd(.6,1.8),a:FX.rnd(.2,.7),p:Math.random()*6});
 for(var i=grains.length-1;i>=0;i--){var p=grains[i];p.x+=p.v*dt;p.y+=Math.sin(t*1.2+p.p)*14*dt;if(m.in||FX.mouse.py<.9){var r=cv.getBoundingClientRect(),dx=p.x-(FX.mouse.x-r.left),dy=p.y-(FX.mouse.y-r.top),q=dx*dx+dy*dy;if(q<16000){var f=(1-q/16000)*260*dt,l=Math.sqrt(q)+1;p.x+=-dy/l*f;p.y+=dx/l*f}}
  if(p.x>w+10){grains.splice(i,1);continue}c.fillStyle=D?'rgba(246,225,180,'+p.a+')':'rgba(120,50,20,'+p.a*.7+')';c.fillRect(p.x,p.y,p.s*2.2,p.s)}
},{still:4,dpr:1.5});
})();
