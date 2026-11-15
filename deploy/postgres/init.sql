-- core, memory, notes and files each get their own database; they could share one (each has its own migrations table).
CREATE DATABASE memory;
CREATE DATABASE notes;
CREATE DATABASE files;
