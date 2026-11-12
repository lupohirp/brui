#!/usr/bin/env node
const path = require('node:path');
const fs = require('node:fs');

const distPath = path.join(__dirname, '../dist/index.js');
if (fs.existsSync(distPath)) {
  require(distPath);
} else {
  try {
    require('tsx/cjs');
    require(path.join(__dirname, '../src/index.ts'));
  } catch {
    require(path.join(__dirname, '../src/index.js'));
  }
}
