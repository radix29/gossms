-- test the long running async execution of a query
declare @x varchar(255)
declare @i int = 0

while @i < 120
begin
    set @i = @i + 1
    waitfor delay '00:00:01'
end

print 'done'
			

/*
test block comments
*/  


SELECT TOP (10)
       o.object_id,
       o.name AS object_name,
       c.column_id,
       c.name AS column_name
FROM sys.objects AS o
JOIN sys.columns AS c
    ON 1 = 1
OPTION (LOOP JOIN);




SELECT TOP (100)
       a.object_id,
       a.name AS object_name_1,
       b.name AS object_name_2,
       c.name AS column_name
FROM sys.all_objects AS a
CROSS JOIN sys.all_objects AS b
CROSS JOIN sys.all_columns AS c
ORDER BY
       CHECKSUM(a.name, b.name, c.name),
       a.modify_date DESC
OPTION (MAXDOP 1);




SELECT
       a.type,
       c.system_type_id,
       COUNT_BIG(*) AS row_count
FROM sys.all_objects AS a
CROSS JOIN sys.all_objects AS b
CROSS JOIN sys.all_columns AS c
GROUP BY
       a.type,
       c.system_type_id
OPTION (HASH GROUP, MAXDOP 1);





SELECT TOP (1000)
       a.object_id,
       a.name AS object_name_1,
       b.object_id AS object_id_2,
       b.name AS object_name_2
FROM sys.all_objects AS a
JOIN sys.all_objects AS b
    ON 1 = 1
ORDER BY
       a.name,
       b.name
OPTION (LOOP JOIN, MAXDOP 1);
