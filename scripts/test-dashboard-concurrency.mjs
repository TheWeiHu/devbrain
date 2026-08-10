import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const source=readFileSync(new URL('../assets/dashboard.js',import.meta.url),'utf8');
const start=source.indexOf('const CONC_TTL=');
const end=source.indexOf('\nfunction chConc()',start);
assert.ok(start>=0&&end>start,'could not extract concurrency helpers from dashboard.js');
new Function(`${source.slice(start,end)};globalThis.concPeaks=concPeaks;globalThis.concPeakLabel=concPeakLabel;`)();
assert.equal(typeof globalThis.concPeaks,'function','concPeaks export is missing');
assert.equal(globalThis.concPeakLabel(20),'20','the cap leaves a peak of 20 unchanged');
assert.equal(globalThis.concPeakLabel(21),'20+','the cap summarizes larger peaks as 20+');

const sequential=globalThis.concPeaks([['a','one',0,100],['b','two',200,300]],0,300,300,1);
assert.equal(sequential.best[0].tot,1,'separate intervals in one display bin are not concurrent');

const overlapping=globalThis.concPeaks([['a','one',0,200],['b','two',100,300]],0,300,300,1);
assert.equal(overlapping.best[0].tot,2,'overlapping intervals retain their real peak');
assert.equal(overlapping.best[0].when,100,'the peak timestamp is the overlap start');

const abutting=globalThis.concPeaks([['a','one',0,100],['b','two',100,200]],0,200,200,1);
assert.equal(abutting.best[0].tot,1,'abutting half-open intervals are not concurrent');

const split=globalThis.concPeaks([['a','one',0,100],['a','one',200,300]],0,300,100,3);
assert.equal(split.best[0].when,0,'a segment records the first bin peak time');
assert.equal(split.best[1],undefined,'empty bins stay empty');
assert.equal(split.best[2].when,200,'a later segment records its own bin peak time');

const proto=globalThis.concPeaks([['__proto__','one',0,100]],0,100,100,1);
assert.equal(proto.best[0].v.__proto__,1,'prototype-like project names remain in the stack');
