# Scratchpad

Scratchpad preserves project knowledge for one owner across their working environments.

## Language

**Owner**:
The person who controls a Scratchpad instance and its project knowledge. Their laptop and VMs access the same owner's knowledge rather than holding separate user accounts.

**GitHub key synchronization**:
Keeping Scratchpad's view of the owner's published keys aligned with their GitHub account. It includes recognizing key removal rather than treating discovery as a one-time import.

**GitHub-managed key**:
A published SSH key whose permission to access the owner's Scratchpad instance follows the linked GitHub account's key set. A machine must prove possession of the matching private key to use that permission.
