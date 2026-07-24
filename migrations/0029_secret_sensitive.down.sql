UPDATE secrets SET sensitive = NOT sensitive;
ALTER TABLE secrets RENAME COLUMN sensitive TO revealed;
