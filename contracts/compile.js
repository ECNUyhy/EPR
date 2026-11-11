const solc = require('solc'); const fs = require('fs');
const input = { language: 'Solidity', sources: { 'D16Router.sol': { content: fs.readFileSync('D16Router.sol', 'utf8') } },
  settings: { optimizer: { enabled: true, runs: 200 }, outputSelection: { '*': { '*': ['abi', 'evm.bytecode.object'] } } } };
const out = JSON.parse(solc.compile(JSON.stringify(input)));
if (out.errors) out.errors.forEach(e => console.error(e.severity, e.formattedMessage));
const c = out.contracts['D16Router.sol']['D16Router'];
fs.writeFileSync('D16Router.json', JSON.stringify({ abi: c.abi, bin: '0x' + c.evm.bytecode.object }));
console.log('bin bytes', c.evm.bytecode.object.length / 2);
