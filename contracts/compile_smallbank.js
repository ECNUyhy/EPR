const solc = require('solc'); const fs = require('fs');
const input = { language: 'Solidity', sources: { 'SmallBank.sol': { content: fs.readFileSync('SmallBank.sol', 'utf8') } },
  settings: { optimizer: { enabled: true, runs: 200 }, outputSelection: { '*': { '*': ['abi', 'evm.bytecode.object'] } } } };
const out = JSON.parse(solc.compile(JSON.stringify(input)));
if (out.errors) out.errors.forEach(e => console.error(e.severity, e.formattedMessage));
const c = out.contracts['SmallBank.sol']['SmallBank'];
fs.writeFileSync('SmallBank.json', JSON.stringify({ abi: c.abi, bin: '0x' + c.evm.bytecode.object }));
console.log('bin bytes', c.evm.bytecode.object.length / 2);
