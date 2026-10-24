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
