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
