import fs from 'node:fs';
import path from 'node:path';

const DOCS_DIR = path.resolve('src/content/docs');
const OUTPUT_FILE = path.resolve('public/llms-full.txt');

function generateLLMsFull() {
  const files = fs.readdirSync(DOCS_DIR).filter(file => file.endsWith('.md'));
  
  const documents = files.map(file => {
    const fullPath = path.join(DOCS_DIR, file);
    const content = fs.readFileSync(fullPath, 'utf8');
    
    // Parse frontmatter
    const match = content.match(/^---\n([\s\S]*?)\n---\n([\s\S]*)$/);
    if (!match) {
      return { file, title: file, section: 'General', order: 99, body: content };
    }
    
    const frontmatterText = match[1];
    const body = match[2].trim();
    
    const titleMatch = frontmatterText.match(/title:\s*["']?([^"'\n]+)["']?/);
    const sectionMatch = frontmatterText.match(/section:\s*["']?([^"'\n]+)["']?/);
    const orderMatch = frontmatterText.match(/order:\s*(\d+)/);
    
    return {
      file,
      title: titleMatch ? titleMatch[1] : file,
      section: sectionMatch ? sectionMatch[1] : 'General',
      order: orderMatch ? parseInt(orderMatch[1], 10) : 99,
      body,
    };
  });

  // Sort by order
  documents.sort((a, b) => a.order - b.order);

  let output = `# Simple Relay Documentation (Full Context)
# Tagline: One binary. No infrastructure. Reliable HTTP delivery.
# URL: https://simplerelay.dev
# Generated for AI Agents and LLMs.

`;

  let currentSection = '';
  for (const doc of documents) {
    if (doc.section !== currentSection) {
      currentSection = doc.section;
      output += `\n${'='.repeat(80)}\n# SECTION: ${currentSection.toUpperCase()}\n${'='.repeat(80)}\n\n`;
    }
    
    output += `## ${doc.title}\n`;
    output += `Source: https://simplerelay.dev/docs/${doc.file.replace('.md', '')}\n\n`;
    output += `${doc.body}\n\n`;
    output += `${'-'.repeat(40)}\n\n`;
  }

  fs.writeFileSync(OUTPUT_FILE, output, 'utf8');
  console.log(`Generated ${OUTPUT_FILE} (${documents.length} documents bundled)`);
}

generateLLMsFull();
