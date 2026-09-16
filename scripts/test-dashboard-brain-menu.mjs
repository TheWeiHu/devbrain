import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import vm from 'node:vm';

const source=readFileSync(new URL('../assets/dashboard.js',import.meta.url),'utf8');
const start=source.indexOf("document.addEventListener('click'");
const end=source.indexOf('const BRAINS_READY=',start);
assert.ok(start>=0 && end>start);
const handlers={}, menu={open:true,contains:()=>false};
let focused=false;
vm.runInNewContext(source.slice(start,end),{
  document:{addEventListener:(event,handler)=>handlers[event]=handler},
  $:selector=>selector==='#brain-controls'?menu:{focus:()=>{focused=true;}},
});

// Selecting a brain replaces the clicked button before the event reaches document.
handlers.click({target:{},composedPath:()=>[{},menu,{}]});
assert.equal(menu.open,true,'Selecting a brain must leave the menu open');
handlers.click({target:{},composedPath:()=>[{}]});
assert.equal(menu.open,false,'Clicking outside must close the menu');
menu.open=true;
handlers.keydown({key:'Tab'});
assert.equal(menu.open,true);
handlers.keydown({key:'Escape'});
assert.equal(menu.open,false);
assert.equal(focused,true,'Escape must return focus to the menu trigger');
